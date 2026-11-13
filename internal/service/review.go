package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"onlineclass/internal/apperr"
	"onlineclass/internal/clock"
	"onlineclass/internal/store"
)

const maxFeedbackRunes = 1000

// ReviewService 负责教师批阅：结论绑定到具体版本，不绑"学生当前状态"。
type ReviewService struct {
	store *store.Store
	clk   *clock.Clock
}

// NewReviewService 构造批阅服务。
func NewReviewService(st *store.Store, clk *clock.Clock) *ReviewService {
	return &ReviewService{store: st, clk: clk}
}

// ReviewInput 是批阅请求体。VersionID 必填：教师批的是"哪一版"必须明确。
type ReviewInput struct {
	VersionID string
	Decision  string
	Feedback  string
	RequestID string
}

// ReviewResult 是批阅结果。Replayed 为 true 表示同一版本同一结论的重复提交。
type ReviewResult struct {
	Review     store.Review
	Enrollment store.Enrollment
	Replayed   bool
	// PointsAwarded 表示本次批阅是否真的新发了积分（重放/非首次通过为 false）。
	PointsAwarded bool
	// Points 是本次发放的分值；未发奖为 0。
	Points int
}

// Review 批阅某个提交版本。
//
// 规则顺序：
//  1. 只有本班教师可批阅；
//  2. 作业必须已发布（草稿没有可批阅的提交）；
//  3. 同版本同结论 → 幂等返回首次结论；同版本异结论 → 409（一版一结论）；
//  4. 只能批阅"当前版本"，旧版本 → 409 stale_version；
//  5. 状态必须是 SUBMITTED，通过 → PASSED，退回 → RETURNED，并在同事务写事件。
func (s *ReviewService) Review(ctx context.Context, actor store.User, assignmentID string, in ReviewInput) (ReviewResult, error) {
	if actor.Role != store.RoleTeacher {
		return ReviewResult{}, apperr.Forbidden("只有教师可以批阅作业")
	}
	a, err := loadAssignmentFor(ctx, s.store, actor, assignmentID)
	if err != nil {
		return ReviewResult{}, err
	}
	if !a.Published() {
		return ReviewResult{}, apperr.Conflict("not_published", "作业尚未发布，没有可批阅的提交")
	}

	versionID, decision, feedback, err := validateReview(in)
	if err != nil {
		return ReviewResult{}, err
	}

	sub, err := s.store.GetSubmissionByVersionID(ctx, assignmentID, versionID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ReviewResult{}, apperr.NotFound("提交版本不存在")
		}
		return ReviewResult{}, err
	}
	enr, err := s.store.GetEnrollment(ctx, assignmentID, sub.StudentID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ReviewResult{}, apperr.NotFound("提交版本不存在")
		}
		return ReviewResult{}, err
	}

	// 幂等优先：已经批过的版本，重试同一结论直接返回首次结论，
	// 不因"它已不是当前版本"而误判为 stale_version。
	if existing, err := s.store.GetReviewByVersionID(ctx, versionID); err == nil {
		return replayReview(existing, decision, enr)
	} else if !errors.Is(err, store.ErrNotFound) {
		return ReviewResult{}, err
	}

	if sub.VersionNo != enr.CurrentVersionNo {
		return ReviewResult{}, apperr.Conflict("stale_version", "只能批阅当前版本，该版本已被新提交取代").
			WithDetails(map[string]any{
				"version_no":         sub.VersionNo,
				"current_version_no": enr.CurrentVersionNo,
			})
	}
	if enr.State != store.StateSubmitted {
		return ReviewResult{}, apperr.Conflict("invalid_state", "当前状态不可批阅").
			WithDetails(map[string]any{"state": enr.State})
	}

	toState := store.StatePassed
	if decision == store.ReviewReject {
		toState = store.StateReturned
	}
	revID, err := newReviewID()
	if err != nil {
		return ReviewResult{}, err
	}
	now := s.clk.Now()
	rev := store.Review{
		ID:        revID,
		VersionID: versionID,
		Decision:  decision,
		Feedback:  feedback,
		TeacherID: actor.ID,
		RequestID: strings.TrimSpace(in.RequestID),
		CreatedAt: now,
	}

	// 通过才发奖，且与批阅同事务落库；退回、重放、非首次通过都不产生流水。
	var award *store.PointsEntry
	if toState == store.StatePassed {
		awardID, err := newPointsID()
		if err != nil {
			return ReviewResult{}, err
		}
		award = &store.PointsEntry{
			ID:           awardID,
			StudentID:    sub.StudentID,
			AssignmentID: assignmentID,
			Reason:       store.ReasonFirstPass,
			Points:       store.PointsFirstPass,
			CreatedAt:    now,
		}
	}

	out, err := s.store.CreateReview(ctx, store.CreateReviewParams{
		Review:            rev,
		AssignmentID:      assignmentID,
		StudentID:         sub.StudentID,
		FromState:         enr.State,
		ToState:           toState,
		ExpectedVersionNo: enr.CurrentVersionNo,
		ActorRole:         actor.Role,
		Award:             award,
	})
	if err != nil {
		switch {
		case errors.Is(err, store.ErrConflict), store.IsUniqueViolation(err):
			// 并发兜底：同一版本可能已被另一个请求批过。
			if existing, getErr := s.store.GetReviewByVersionID(ctx, versionID); getErr == nil {
				return replayReview(existing, decision, enr)
			}
			return ReviewResult{}, apperr.Conflict("version_conflict", "批阅状态已被并发修改，请刷新后重试")
		default:
			return ReviewResult{}, err
		}
	}

	return ReviewResult{
		Review: rev,
		Enrollment: store.Enrollment{
			AssignmentID:     assignmentID,
			StudentID:        sub.StudentID,
			State:            toState,
			StateVersion:     enr.StateVersion + 1,
			CurrentVersionNo: enr.CurrentVersionNo,
		},
		PointsAwarded: out.Awarded,
		Points:        pointsIf(out.Awarded),
	}, nil
}

// pointsIf 把"是否发奖"翻译成分值，避免调用方自己写 10/0 的魔法数。
func pointsIf(awarded bool) int {
	if awarded {
		return store.PointsFirstPass
	}
	return 0
}

// replayReview 处理"该版本已有结论"：同结论视为重放，异结论明确拒绝。
func replayReview(existing store.Review, decision string, enr store.Enrollment) (ReviewResult, error) {
	if existing.Decision != decision {
		return ReviewResult{}, apperr.Conflict("review_conflict", "该版本已有不同结论，不能重复批阅").
			WithDetails(map[string]any{
				"existing_decision":  existing.Decision,
				"requested_decision": decision,
			})
	}
	return ReviewResult{Review: existing, Enrollment: enr, Replayed: true}, nil
}

func validateReview(in ReviewInput) (string, string, string, error) {
	versionID := strings.TrimSpace(in.VersionID)
	decision := strings.TrimSpace(in.Decision)
	feedback := strings.TrimSpace(in.Feedback)
	switch {
	case versionID == "":
		return "", "", "", apperr.Invalid("version_id 不能为空：批阅必须指明版本")
	case decision != store.ReviewPass && decision != store.ReviewReject:
		return "", "", "", apperr.Invalid("decision 只能是 pass 或 reject")
	case decision == store.ReviewReject && feedback == "":
		return "", "", "", apperr.Invalid("退回必须填写反馈原因")
	case utf8.RuneCountInString(feedback) > maxFeedbackRunes:
		return "", "", "", apperr.Invalid("反馈不能超过 1000 个字符")
	}
	return versionID, decision, feedback, nil
}

func newReviewID() (string, error) {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成批阅编号失败: %w", err)
	}
	return "RV-" + hex.EncodeToString(buf), nil
}

func newPointsID() (string, error) {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成积分流水编号失败: %w", err)
	}
	return "PT-" + hex.EncodeToString(buf), nil
}
