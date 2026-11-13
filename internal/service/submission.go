package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"onlineclass/internal/apperr"
	"onlineclass/internal/clock"
	"onlineclass/internal/store"
)

const (
	maxContentRunes = 5000
	maxRequestIDLen = 64
)

// SubmissionService 负责学生提交与版本查询。
type SubmissionService struct {
	store *store.Store
	clk   *clock.Clock
}

// NewSubmissionService 构造提交服务。
func NewSubmissionService(st *store.Store, clk *clock.Clock) *SubmissionService {
	return &SubmissionService{store: st, clk: clk}
}

// SubmitInput 是提交请求体。
type SubmitInput struct {
	Content   string
	RequestID string
	// BaseVersion 是客户端"提交时看到的当前版本号"，首次提交传 0。
	// 为 nil 表示请求未携带，按 400 处理：不能靠默认值蒙混成"基于最新版"。
	BaseVersion *int
}

// SubmitResult 是提交成功后的返回信息。
type SubmitResult struct {
	Version           store.Submission
	Enrollment        store.Enrollment
	EffectiveDeadline time.Time
	ExtensionApplied  bool
	// Replayed 为 true 表示命中幂等重放：没有新增版本，返回的是首次结果。
	Replayed bool
}

// SubmissionHistory 是某学生在一份作业上的版本历史。
type SubmissionHistory struct {
	AssignmentID      string
	StudentID         string
	State             string
	CurrentVersionNo  int
	EffectiveDeadline time.Time
	ExtensionApplied  bool
	Versions          []store.Submission
}

// Submit 追加一个提交版本。
//
// 规则顺序（先判身份与资源，再判状态与时间，最后才是写入）：
//  1. 只有本班学生本人可提交；
//  2. 作业必须已发布（草稿对学生按不存在处理）；
//  3. 已通过（PASSED）不得再提交；
//  4. 超过有效截止时间（含个人补交授权）不得提交；恰好等于截止时刻仍允许，
//     即判定条件为 now > deadline；
//  5. 幂等：同 request_id + 同内容 → 返回首次版本（Replayed=true），不新增；
//     同 request_id + 异内容 → 409 idempotency_conflict；
//  6. 旧请求保护：base_version 与服务端当前版本不一致 → 409 stale_version，
//     不写入任何内容（旧页面无法覆盖新提交）。
func (s *SubmissionService) Submit(ctx context.Context, actor store.User, assignmentID string, in SubmitInput) (SubmitResult, error) {
	if actor.Role != store.RoleStudent {
		return SubmitResult{}, apperr.Forbidden("只有学生可以提交作业")
	}
	a, err := loadAssignmentFor(ctx, s.store, actor, assignmentID)
	if err != nil {
		return SubmitResult{}, err
	}
	if !a.Published() {
		return SubmitResult{}, apperr.NotFound("作业不存在")
	}

	enr, err := s.store.GetEnrollment(ctx, assignmentID, actor.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return SubmitResult{}, apperr.Conflict("not_enrolled", "你不在该作业的学生名单中，无法提交")
		}
		return SubmitResult{}, err
	}
	if enr.State == store.StatePassed {
		return SubmitResult{}, apperr.Conflict("already_passed", "作业已通过，不能再次提交").
			WithDetails(map[string]any{"state": enr.State})
	}

	content, requestID, baseVersion, err := validateSubmission(in)
	if err != nil {
		return SubmitResult{}, err
	}
	contentHash := hashContent(content)

	deadline, extended, err := s.effectiveDeadline(ctx, a, actor.ID)
	if err != nil {
		return SubmitResult{}, err
	}
	now := s.clk.Now()
	if now.After(deadline) {
		return SubmitResult{}, apperr.Conflict("deadline_passed", "已超过截止时间，不能提交").
			WithDetails(map[string]any{
				"effective_deadline": deadline.Format(time.RFC3339),
				"extension_applied":  extended,
			})
	}

	// 幂等重放：同一 request_id 已经成功过，就不再看 base_version（首次结果已生效），
	// 同内容返回首次版本，异内容明确拒绝，避免"同号改内容"悄悄覆盖。
	if existing, err := s.store.GetSubmissionByRequestID(ctx, assignmentID, actor.ID, requestID); err == nil {
		return replayResult(existing, contentHash, enr, deadline, extended)
	} else if !errors.Is(err, store.ErrNotFound) {
		return SubmitResult{}, err
	}

	// 旧请求保护：客户端看到的版本必须等于服务端当前版本，否则说明页面过期。
	if baseVersion != enr.CurrentVersionNo {
		return SubmitResult{}, apperr.Conflict("stale_version", "提交基于旧版本，请刷新后重试").
			WithDetails(map[string]any{
				"base_version":       baseVersion,
				"current_version_no": enr.CurrentVersionNo,
			})
	}

	subID, err := newSubmissionID()
	if err != nil {
		return SubmitResult{}, err
	}
	sub := store.Submission{
		ID:           subID,
		AssignmentID: assignmentID,
		StudentID:    actor.ID,
		VersionNo:    enr.CurrentVersionNo + 1,
		Content:      content,
		ContentHash:  contentHash,
		RequestID:    requestID,
		CreatedAt:    now,
	}

	err = s.store.SubmitVersion(ctx, store.SubmitVersionParams{
		Submission:        sub,
		FromState:         enr.State,
		ToState:           store.StateSubmitted,
		ExpectedVersionNo: enr.CurrentVersionNo,
		ActorRole:         actor.Role,
	})
	if err != nil {
		switch {
		case errors.Is(err, store.ErrConflict), store.IsUniqueViolation(err):
			// 并发兜底：可能是"同一 request_id 已被另一个连接写入"，
			// 再查一次以兑现幂等语义；否则说明版本已被人推进，让客户端刷新。
			if existing, getErr := s.store.GetSubmissionByRequestID(ctx, assignmentID, actor.ID, requestID); getErr == nil {
				return replayResult(existing, contentHash, enr, deadline, extended)
			}
			return SubmitResult{}, apperr.Conflict("version_conflict", "提交状态已被并发修改，请刷新后重试")
		default:
			return SubmitResult{}, err
		}
	}

	return SubmitResult{
		Version: sub,
		Enrollment: store.Enrollment{
			AssignmentID:     assignmentID,
			StudentID:        actor.ID,
			State:            store.StateSubmitted,
			StateVersion:     enr.StateVersion + 1,
			CurrentVersionNo: sub.VersionNo,
		},
		EffectiveDeadline: deadline,
		ExtensionApplied:  extended,
	}, nil
}

// History 返回某学生在一份作业上的全部版本。
//
// 学生只能查自己；教师与助教可查本班学生，跨班一律 404。
func (s *SubmissionService) History(ctx context.Context, actor store.User, assignmentID, studentID string) (SubmissionHistory, error) {
	if studentID == "" {
		studentID = actor.ID
	}
	a, err := loadAssignmentFor(ctx, s.store, actor, assignmentID)
	if err != nil {
		return SubmissionHistory{}, err
	}
	if !a.Published() && actor.Role != store.RoleTeacher {
		return SubmissionHistory{}, apperr.NotFound("作业不存在")
	}
	if err := s.ensureCanViewStudent(ctx, actor, studentID); err != nil {
		return SubmissionHistory{}, err
	}

	deadline, extended, err := s.effectiveDeadline(ctx, a, studentID)
	if err != nil {
		return SubmissionHistory{}, err
	}

	enr, err := s.store.GetEnrollment(ctx, assignmentID, studentID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return SubmissionHistory{}, apperr.NotFound("该学生不在作业名单中")
		}
		return SubmissionHistory{}, err
	}
	versions, err := s.store.ListSubmissions(ctx, assignmentID, studentID)
	if err != nil {
		return SubmissionHistory{}, err
	}
	return SubmissionHistory{
		AssignmentID:      assignmentID,
		StudentID:         studentID,
		State:             enr.State,
		CurrentVersionNo:  enr.CurrentVersionNo,
		EffectiveDeadline: deadline,
		ExtensionApplied:  extended,
		Versions:          versions,
	}, nil
}

// ensureCanViewStudent 校验查看他人提交的权限：学生仅本人，教师/助教限本班学生。
func (s *SubmissionService) ensureCanViewStudent(ctx context.Context, actor store.User, studentID string) error {
	if actor.Role == store.RoleStudent {
		if studentID != actor.ID {
			return apperr.Forbidden("学生只能查看自己的提交")
		}
		return nil
	}
	target, err := s.store.GetUser(ctx, studentID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apperr.NotFound("学生不存在")
		}
		return err
	}
	if target.ClassID != actor.ClassID || target.Role != store.RoleStudent {
		return apperr.NotFound("学生不存在")
	}
	return nil
}

// effectiveDeadline 返回该生的有效截止时间：有补交授权则取授权时间，否则取作业基准快照。
func (s *SubmissionService) effectiveDeadline(ctx context.Context, a store.Assignment, studentID string) (time.Time, bool, error) {
	ext, err := s.store.GetExtension(ctx, a.ID, studentID)
	if err == nil {
		return ext.DeadlineAt, true, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return time.Time{}, false, err
	}
	return a.EffectiveDeadline(), false, nil
}

// replayResult 处理"request_id 已存在"：同内容视为重放，异内容视为冲突。
func replayResult(existing store.Submission, contentHash string, enr store.Enrollment, deadline time.Time, extended bool) (SubmitResult, error) {
	if existing.ContentHash != contentHash {
		return SubmitResult{}, apperr.Conflict("idempotency_conflict", "同一个 request_id 提交了不同内容").
			WithDetails(map[string]any{"existing_version_no": existing.VersionNo})
	}
	return SubmitResult{
		Version:           existing,
		Enrollment:        enr,
		EffectiveDeadline: deadline,
		ExtensionApplied:  extended,
		Replayed:          true,
	}, nil
}

func validateSubmission(in SubmitInput) (string, string, int, error) {
	content := strings.TrimSpace(in.Content)
	requestID := strings.TrimSpace(in.RequestID)
	switch {
	case content == "":
		return "", "", 0, apperr.Invalid("提交内容不能为空")
	case utf8.RuneCountInString(content) > maxContentRunes:
		return "", "", 0, apperr.Invalid("提交内容不能超过 5000 个字符")
	case requestID == "":
		return "", "", 0, apperr.Invalid("request_id 不能为空：用于识别重复提交")
	case len(requestID) > maxRequestIDLen:
		return "", "", 0, apperr.Invalid("request_id 不能超过 64 个字符")
	case in.BaseVersion == nil:
		return "", "", 0, apperr.Invalid("base_version 不能为空：首次提交传 0，更新时传当前版本号")
	case *in.BaseVersion < 0:
		return "", "", 0, apperr.Invalid("base_version 不能为负数")
	}
	return content, requestID, *in.BaseVersion, nil
}

func hashContent(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func newSubmissionID() (string, error) {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成提交编号失败: %w", err)
	}
	return "SUB-" + hex.EncodeToString(buf), nil
}
