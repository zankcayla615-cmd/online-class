package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"onlineclass/internal/store"
)

func newAssignment(id, classID string) store.Assignment {
	return store.Assignment{
		ID:          id,
		ClassID:     classID,
		TeacherID:   "T01",
		Title:       "原始标题",
		Requirement: "原始要求",
		DeadlineAt:  time.Date(2026, 12, 1, 10, 0, 0, 0, time.UTC),
		State:       store.AssignmentDraft,
		CreatedAt:   time.Date(2026, 11, 13, 8, 0, 0, 0, time.UTC),
	}
}

func TestCreateAndUpdateDraft(t *testing.T) {
	st := openTestStore(t)
	mustMigrateSeed(t, st)
	ctx := context.Background()

	a := newAssignment("HW1", "P01")
	if err := st.CreateAssignment(ctx, a); err != nil {
		t.Fatalf("创建作业失败: %v", err)
	}
	got, err := st.GetAssignment(ctx, "HW1")
	if err != nil {
		t.Fatalf("读取作业失败: %v", err)
	}
	if got.State != store.AssignmentDraft || got.Title != "原始标题" {
		t.Fatalf("创建结果不符: %+v", got)
	}

	newDeadline := time.Date(2026, 12, 5, 10, 0, 0, 0, time.UTC)
	if err := st.UpdateAssignmentDraft(ctx, "HW1", "新标题", "新要求", newDeadline); err != nil {
		t.Fatalf("更新草稿失败: %v", err)
	}
	got, err = st.GetAssignment(ctx, "HW1")
	if err != nil {
		t.Fatalf("读取作业失败: %v", err)
	}
	if got.Title != "新标题" || got.Requirement != "新要求" || !got.DeadlineAt.Equal(newDeadline) {
		t.Fatalf("更新未生效: %+v", got)
	}

	if err := st.UpdateAssignmentDraft(ctx, "NOPE", "标题", "要求", newDeadline); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("不存在作业应返回 ErrNotFound，实际 %v", err)
	}
}

func TestUpdateDraftAfterPublishConflicts(t *testing.T) {
	st := openTestStore(t)
	mustMigrateSeed(t, st)
	ctx := context.Background()

	if err := st.CreateAssignment(ctx, newAssignment("HW1", "P01")); err != nil {
		t.Fatalf("创建作业失败: %v", err)
	}
	if err := st.PublishAssignment(ctx, "HW1", "T01", time.Now().UTC()); err != nil {
		t.Fatalf("发布失败: %v", err)
	}
	err := st.UpdateAssignmentDraft(ctx, "HW1", "改标题", "改要求", time.Date(2026, 12, 9, 0, 0, 0, 0, time.UTC))
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("已发布作业更新应返回 ErrConflict，实际 %v", err)
	}
	got, err := st.GetAssignment(ctx, "HW1")
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if got.Title != "原始标题" {
		t.Fatalf("冲突更新不得改内容，实际 %s", got.Title)
	}
}

func TestPublishAssignment(t *testing.T) {
	st := openTestStore(t)
	mustMigrateSeed(t, st)
	ctx := context.Background()

	if err := st.CreateAssignment(ctx, newAssignment("HW1", "P01")); err != nil {
		t.Fatalf("创建作业失败: %v", err)
	}
	at := time.Date(2026, 11, 13, 9, 30, 0, 0, time.UTC)
	if err := st.PublishAssignment(ctx, "HW1", "T01", at); err != nil {
		t.Fatalf("发布失败: %v", err)
	}

	got, err := st.GetAssignment(ctx, "HW1")
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if got.State != store.AssignmentPublished {
		t.Fatalf("状态应为 PUBLISHED，实际 %s", got.State)
	}
	if got.SnapshotTitle != "原始标题" || got.SnapshotRequirement != "原始要求" {
		t.Fatalf("快照内容不符: %s / %s", got.SnapshotTitle, got.SnapshotRequirement)
	}
	if !got.SnapshotDeadlineAt.Equal(got.DeadlineAt) {
		t.Fatalf("快照截止时间应等于发布时截止时间: %v vs %v", got.SnapshotDeadlineAt, got.DeadlineAt)
	}
	if got.SnapshotBy != "T01" || !got.SnapshotAt.Equal(at) || !got.PublishedAt.Equal(at) {
		t.Fatalf("快照元信息不符: by=%s at=%v published=%v", got.SnapshotBy, got.SnapshotAt, got.PublishedAt)
	}

	n, err := st.CountEnrollments(ctx, "HW1")
	if err != nil {
		t.Fatalf("统计状态行失败: %v", err)
	}
	if n != 5 {
		t.Fatalf("应为 P01 五名学生建状态行，实际 %d", n)
	}

	// 重复发布：数据不变、返回冲突
	if err := st.PublishAssignment(ctx, "HW1", "T01", at.Add(time.Hour)); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("重复发布应返回 ErrConflict，实际 %v", err)
	}
	again, err := st.GetAssignment(ctx, "HW1")
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if !again.SnapshotAt.Equal(at) {
		t.Fatalf("重复发布不得改写快照时间，实际 %v", again.SnapshotAt)
	}

	if err := st.PublishAssignment(ctx, "NOPE", "T01", at); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("不存在作业应返回 ErrNotFound，实际 %v", err)
	}
}

// P02 只有 1 名学生，验证状态行按班级花名册创建而不是全班所有人。
func TestPublishEnrollsOnlyClassStudents(t *testing.T) {
	st := openTestStore(t)
	mustMigrateSeed(t, st)
	ctx := context.Background()

	a := newAssignment("HW9", "P02")
	if err := st.CreateAssignment(ctx, a); err != nil {
		t.Fatalf("创建作业失败: %v", err)
	}
	if err := st.PublishAssignment(ctx, "HW9", "T01", time.Now().UTC()); err != nil {
		t.Fatalf("发布失败: %v", err)
	}
	n, err := st.CountEnrollments(ctx, "HW9")
	if err != nil {
		t.Fatalf("统计失败: %v", err)
	}
	if n != 1 {
		t.Fatalf("P02 应只有 1 条状态行，实际 %d", n)
	}
}
