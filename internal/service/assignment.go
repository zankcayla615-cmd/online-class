// Package service 承载业务规则与事务边界；handler 与 store 都不写业务判断。
package service

import (
	"context"
	"crypto/rand"
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

// AssignmentInput 是创建/修改草稿的入参。
type AssignmentInput struct {
	Title       string
	Requirement string
	DeadlineAt  time.Time
}

// AssignmentService 负责作业的创建、草稿修改与发布。
type AssignmentService struct {
	store *store.Store
	clk   *clock.Clock
}

// NewAssignmentService 构造作业服务。
func NewAssignmentService(st *store.Store, clk *clock.Clock) *AssignmentService {
	return &AssignmentService{store: st, clk: clk}
}

// 校验长度按"字符数"而非字节数，避免中文被误判超长。
const (
	maxTitleRunes       = 100
	maxRequirementRunes = 2000
)

// Create 创建草稿。仅本班教师可操作，班级与作者取自会话身份，不信任请求体。
func (s *AssignmentService) Create(ctx context.Context, actor store.User, in AssignmentInput) (store.Assignment, error) {
	if actor.Role != store.RoleTeacher {
		return store.Assignment{}, apperr.Forbidden("只有教师可以创建作业")
	}
	title, requirement, deadline, err := s.validate(in)
	if err != nil {
		return store.Assignment{}, err
	}

	id, err := newAssignmentID()
	if err != nil {
		return store.Assignment{}, err
	}
	a := store.Assignment{
		ID:          id,
		ClassID:     actor.ClassID,
		TeacherID:   actor.ID,
		Title:       title,
		Requirement: requirement,
		DeadlineAt:  deadline,
		State:       store.AssignmentDraft,
		CreatedAt:   s.clk.Now(),
	}
	if err := s.store.CreateAssignment(ctx, a); err != nil {
		return store.Assignment{}, err
	}
	return a, nil
}

// UpdateDraft 修改草稿。已发布作业拒绝修改（409 assignment_locked），不做静默变更。
func (s *AssignmentService) UpdateDraft(ctx context.Context, actor store.User, id string, in AssignmentInput) (store.Assignment, error) {
	if actor.Role != store.RoleTeacher {
		return store.Assignment{}, apperr.Forbidden("只有教师可以修改作业")
	}
	current, err := s.loadForActor(ctx, actor, id)
	if err != nil {
		return store.Assignment{}, err
	}
	if current.Published() {
		return store.Assignment{}, apperr.Conflict("assignment_locked", "作业已发布，内容已锁定，不能修改").
			WithDetails(map[string]any{"published_at": current.PublishedAt.Format(time.RFC3339)})
	}

	title, requirement, deadline, err := s.validate(in)
	if err != nil {
		return store.Assignment{}, err
	}
	if err := s.store.UpdateAssignmentDraft(ctx, id, title, requirement, deadline); err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			return store.Assignment{}, apperr.NotFound("作业不存在")
		case errors.Is(err, store.ErrConflict):
			// 并发下作业刚被发布：同样按锁定处理，并给出实际状态。
			return store.Assignment{}, apperr.Conflict("assignment_locked", "作业已被发布，修改未生效")
		default:
			return store.Assignment{}, err
		}
	}
	return s.store.GetAssignment(ctx, id)
}

// Publish 发布作业并生成快照。重复发布返回 409 already_published。
func (s *AssignmentService) Publish(ctx context.Context, actor store.User, id string) (store.Assignment, error) {
	if actor.Role != store.RoleTeacher {
		return store.Assignment{}, apperr.Forbidden("只有教师可以发布作业")
	}
	current, err := s.loadForActor(ctx, actor, id)
	if err != nil {
		return store.Assignment{}, err
	}
	if current.Published() {
		return store.Assignment{}, apperr.Conflict("already_published", "作业已发布，请勿重复发布").
			WithDetails(map[string]any{"published_at": current.PublishedAt.Format(time.RFC3339)})
	}

	now := s.clk.Now()
	if err := s.store.PublishAssignment(ctx, id, actor.ID, now); err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			return store.Assignment{}, apperr.NotFound("作业不存在")
		case errors.Is(err, store.ErrConflict):
			return store.Assignment{}, apperr.Conflict("already_published", "作业已被发布，请勿重复发布")
		default:
			return store.Assignment{}, err
		}
	}
	return s.store.GetAssignment(ctx, id)
}

// loadForActor 读取作业并做班级隔离：跨班与不存在统一 404。
func (s *AssignmentService) loadForActor(ctx context.Context, actor store.User, id string) (store.Assignment, error) {
	a, err := s.store.GetAssignment(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.Assignment{}, apperr.NotFound("作业不存在")
		}
		return store.Assignment{}, err
	}
	if a.ClassID != actor.ClassID {
		return store.Assignment{}, apperr.NotFound("作业不存在")
	}
	return a, nil
}

func (s *AssignmentService) validate(in AssignmentInput) (string, string, time.Time, error) {
	title := strings.TrimSpace(in.Title)
	requirement := strings.TrimSpace(in.Requirement)
	switch {
	case title == "":
		return "", "", time.Time{}, apperr.Invalid("标题不能为空")
	case utf8.RuneCountInString(title) > maxTitleRunes:
		return "", "", time.Time{}, apperr.Invalid("标题不能超过 100 个字符")
	case requirement == "":
		return "", "", time.Time{}, apperr.Invalid("提交要求不能为空")
	case utf8.RuneCountInString(requirement) > maxRequirementRunes:
		return "", "", time.Time{}, apperr.Invalid("提交要求不能超过 2000 个字符")
	case in.DeadlineAt.IsZero():
		return "", "", time.Time{}, apperr.Invalid("截止时间不能为空")
	case !in.DeadlineAt.After(s.clk.Now()):
		return "", "", time.Time{}, apperr.Invalid("截止时间必须晚于当前业务时间")
	}
	return title, requirement, in.DeadlineAt.UTC(), nil
}

// newAssignmentID 生成作业编号（HW- + 8 位十六进制），不依赖数据库自增，
// 也不使用系统时间（业务时间必须来自注入的 clock）。
func newAssignmentID() (string, error) {
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成作业编号失败: %w", err)
	}
	return "HW-" + hex.EncodeToString(buf), nil
}
