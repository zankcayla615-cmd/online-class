package store_test

import (
	"context"
	"errors"
	"testing"

	"onlineclass/internal/store"
)

func TestGetUser(t *testing.T) {
	st := openTestStore(t)
	mustMigrateSeed(t, st)
	ctx := context.Background()

	u, err := st.GetUser(ctx, "T01")
	if err != nil {
		t.Fatalf("查询教师失败: %v", err)
	}
	if u.Role != store.RoleTeacher || u.ClassID != "P01" || u.Name == "" {
		t.Fatalf("教师信息异常: %+v", u)
	}

	if _, err := st.GetUser(ctx, "NOPE"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("不存在账号应返回 ErrNotFound，实际 %v", err)
	}
}

func TestListStudents(t *testing.T) {
	st := openTestStore(t)
	mustMigrateSeed(t, st)
	ctx := context.Background()

	got, err := st.ListStudents(ctx, "P01")
	if err != nil {
		t.Fatalf("查询 P01 学生失败: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("P01 应有 5 名学生，实际 %d", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].ID >= got[i].ID {
			t.Fatalf("应按编号升序返回: %s >= %s", got[i-1].ID, got[i].ID)
		}
	}
	if got[0].Role != store.RoleStudent {
		t.Fatalf("返回对象应只含学生，实际角色 %s", got[0].Role)
	}

	other, err := st.ListStudents(ctx, "P02")
	if err != nil {
		t.Fatalf("查询 P02 学生失败: %v", err)
	}
	if len(other) != 1 || other[0].ID != "X01" {
		t.Fatalf("P02 应只有 X01，实际 %+v", other)
	}
}

func TestGetAssignment(t *testing.T) {
	st := openTestStore(t)
	mustMigrateSeed(t, st)
	ctx := context.Background()

	_, err := st.GetAssignment(ctx, "NOPE")
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("不存在作业应返回 ErrNotFound，实际 %v", err)
	}

	if _, err := st.DB().Exec(`INSERT INTO assignments
		(id, class_id, teacher_id, title, requirement, deadline_at, state,
		 published_at, snapshot_title, snapshot_requirement, snapshot_deadline_at, snapshot_by, snapshot_at, created_at)
		VALUES ('HW1','P01','T01','草稿标题','草稿要求','2026-12-01T10:00:00Z','PUBLISHED',
		 '2026-11-13T09:00:00Z','快照标题','快照要求','2026-12-02T10:00:00Z','T01','2026-11-13T09:00:00Z','2026-11-13T08:00:00Z')`); err != nil {
		t.Fatalf("插入作业失败: %v", err)
	}

	a, err := st.GetAssignment(ctx, "HW1")
	if err != nil {
		t.Fatalf("查询作业失败: %v", err)
	}
	if !a.Published() {
		t.Fatal("作业应为已发布")
	}
	if a.EffectiveTitle() != "快照标题" || a.EffectiveRequirement() != "快照要求" {
		t.Fatalf("已发布作业应以快照为准，实际 %s / %s", a.EffectiveTitle(), a.EffectiveRequirement())
	}
	if a.EffectiveDeadline().UTC().Format("2006-01-02T15:04:05Z") != "2026-12-02T10:00:00Z" {
		t.Fatalf("截止时间应取快照，实际 %v", a.EffectiveDeadline())
	}
	if a.SnapshotBy != "T01" || a.SnapshotAt.IsZero() {
		t.Fatalf("快照元信息缺失: %+v", a)
	}
}

func TestListAssignmentsByClass(t *testing.T) {
	st := openTestStore(t)
	mustMigrateSeed(t, st)
	ctx := context.Background()

	for _, row := range []struct{ id, class, created string }{
		{"HW1", "P01", "2026-11-13T08:00:00Z"},
		{"HW2", "P01", "2026-11-13T09:00:00Z"},
		{"HW3", "P02", "2026-11-13T10:00:00Z"},
	} {
		if _, err := st.DB().Exec(`INSERT INTO assignments
			(id, class_id, teacher_id, title, requirement, deadline_at, state, created_at)
			VALUES (?, ?, 'T01', '标题', '要求', '2026-12-01T00:00:00Z', 'DRAFT', ?)`,
			row.id, row.class, row.created); err != nil {
			t.Fatalf("插入作业失败: %v", err)
		}
	}

	list, err := st.ListAssignmentsByClass(ctx, "P01")
	if err != nil {
		t.Fatalf("查询班级作业失败: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("P01 应有 2 份作业，实际 %d", len(list))
	}
	if list[0].ID != "HW2" || list[1].ID != "HW1" {
		t.Fatalf("应按创建时间倒序，实际 %s, %s", list[0].ID, list[1].ID)
	}
	if list[0].Published() {
		t.Fatal("草稿作业不应视为已发布")
	}
	if list[0].EffectiveTitle() != "标题" {
		t.Fatalf("草稿应展示实时标题，实际 %s", list[0].EffectiveTitle())
	}
}
