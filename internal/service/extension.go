package service

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"onlineclass/internal/apperr"
	"onlineclass/internal/clock"
	"onlineclass/internal/store"
)

// 补交原因长度按字符数计：FR-06 要求 ≥5 且 ≤200。
const (
	minExtensionReasonRunes = 5
	maxExtensionReasonRunes = 200
)

// ExtensionService 负责"按人、一次性"的补交授权。
type ExtensionService struct {
	store *store.Store
	clk   *clock.Clock
}

// NewExtensionService 构造补交服务。
func NewExtensionService(st *store.Store, clk *clock.Clock) *ExtensionService {
	return &ExtensionService{store: st, clk: clk}
}

// ExtensionInput 是补交请求体。
type ExtensionInput struct {
	StudentID  string
	DeadlineAt time.Time
	Reason     string
}

// Grant 为某学生设置一次性补交截止。
//
// 规则顺序：
//  1. 只有本班教师可批准；
//  2. 作业必须已发布（草稿没有名单，也没有可补交的提交）；
//  3. 目标必须是本班学生且在该作业名单中；
//  4. 已通过（PASSED）的学生无需补交；
//  5. 每个学生每份作业只能一次，重复 → 409 extension_exists；
//  6. 补交截止必须晚于原截止，只影响该生。
func (s *ExtensionService) Grant(ctx context.Context, actor store.User, assignmentID string, in ExtensionInput) (store.Extension, error) {
	if actor.Role != store.RoleTeacher {
		return store.Extension{}, apperr.Forbidden("只有教师可以批准补交")
	}
	a, err := loadAssignmentFor(ctx, s.store, actor, assignmentID)
	if err != nil {
		return store.Extension{}, err
	}
	if !a.Published() {
		return store.Extension{}, apperr.Conflict("not_published", "作业尚未发布，暂无学生名单可批补交")
	}

	studentID, deadline, reason, err := validateExtension(in)
	if err != nil {
		return store.Extension{}, err
	}

	target, err := s.store.GetUser(ctx, studentID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.Extension{}, apperr.NotFound("学生不存在")
		}
		return store.Extension{}, err
	}
	// 跨班或非学生账号一律按"不存在"处理，避免通过补交接口探测他人身份。
	if target.ClassID != actor.ClassID || target.Role != store.RoleStudent {
		return store.Extension{}, apperr.NotFound("学生不存在")
	}

	enr, err := s.store.GetEnrollment(ctx, assignmentID, studentID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.Extension{}, apperr.Conflict("not_enrolled", "该学生不在作业名单中").
				WithDetails(map[string]any{"student_id": studentID})
		}
		return store.Extension{}, err
	}
	if enr.State == store.StatePassed {
		return store.Extension{}, apperr.Conflict("already_passed", "该学生已通过，无需补交").
			WithDetails(map[string]any{"state": enr.State})
	}

	base := a.EffectiveDeadline()
	if !deadline.After(base) {
		return store.Extension{}, apperr.New(400, "extension_deadline_invalid", "补交截止必须晚于原截止时间").
			WithDetails(map[string]any{
				"deadline_at":          deadline.Format(time.RFC3339),
				"original_deadline_at": base.Format(time.RFC3339),
			})
	}

	// 先查一次给出友好冲突；并发下靠主键兜底。
	if existing, err := s.store.GetExtension(ctx, assignmentID, studentID); err == nil {
		return store.Extension{}, extensionExists(existing)
	} else if !errors.Is(err, store.ErrNotFound) {
		return store.Extension{}, err
	}

	ext := store.Extension{
		AssignmentID: assignmentID,
		StudentID:    studentID,
		DeadlineAt:   deadline,
		Reason:       reason,
		GrantedBy:    actor.ID,
		GrantedAt:    s.clk.Now(),
	}
	err = s.store.CreateExtension(ctx, store.CreateExtensionParams{
		Extension: ext,
		State:     enr.State,
		ActorRole: actor.Role,
	})
	if err != nil {
		if store.IsUniqueViolation(err) {
			// 并发下另一个请求刚写入：按"已存在"返回，保证语义一致。
			if existing, getErr := s.store.GetExtension(ctx, assignmentID, studentID); getErr == nil {
				return store.Extension{}, extensionExists(existing)
			}
			return store.Extension{}, apperr.Conflict("extension_exists", "该学生已设置过补交").
				WithDetails(map[string]any{"student_id": studentID})
		}
		return store.Extension{}, err
	}
	return ext, nil
}

func extensionExists(existing store.Extension) error {
	return apperr.Conflict("extension_exists", "每个学生每份作业只能设置一次补交").
		WithDetails(map[string]any{
			"deadline_at": existing.DeadlineAt.Format(time.RFC3339),
			"granted_at":  existing.GrantedAt.Format(time.RFC3339),
		})
}

func validateExtension(in ExtensionInput) (string, time.Time, string, error) {
	studentID := strings.TrimSpace(in.StudentID)
	reason := strings.TrimSpace(in.Reason)
	runes := utf8.RuneCountInString(reason)
	switch {
	case studentID == "":
		return "", time.Time{}, "", apperr.Invalid("student_id 不能为空")
	case in.DeadlineAt.IsZero():
		return "", time.Time{}, "", apperr.Invalid("deadline_at 不能为空：补交必须给出新的截止时间")
	case runes < minExtensionReasonRunes:
		return "", time.Time{}, "", apperr.Invalid("补交原因至少 5 个字")
	case runes > maxExtensionReasonRunes:
		return "", time.Time{}, "", apperr.Invalid("补交原因不能超过 200 个字")
	}
	return studentID, in.DeadlineAt.UTC(), reason, nil
}
