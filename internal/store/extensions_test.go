package store_test

import (
	"context"
	"testing"
	"time"

	"onlineclass/internal/store"
)

func sampleExtension(studentID string) store.Extension {
	return store.Extension{
		AssignmentID: "HW1",
		StudentID:    studentID,
		DeadlineAt:   time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC),
		Reason:       "生病住院，需要补交",
		GrantedBy:    "T01",
		GrantedAt:    time.Date(2026, 11, 13, 11, 0, 0, 0, time.UTC),
	}
}

func TestCreateExtensionWritesEventAndBlocksSecond(t *testing.T) {
	st := openTestStore(t)
	mustMigrateSeed(t, st)
	ctx := context.Background()
	seedPublishedWithEnrollments(t, st, "HW1")

	err := st.CreateExtension(ctx, store.CreateExtensionParams{
		Extension: sampleExtension("S01"),
		State:     store.StateNotSubmitted,
		ActorRole: store.RoleTeacher,
	})
	if err != nil {
		t.Fatalf("写入补交失败: %v", err)
	}

	ext, err := st.GetExtension(ctx, "HW1", "S01")
	if err != nil {
		t.Fatalf("读取补交失败: %v", err)
	}
	if ext.Reason != "生病住院，需要补交" || ext.GrantedBy != "T01" {
		t.Fatalf("补交记录不符: %+v", ext)
	}

	list, err := st.ListExtensions(ctx, "HW1")
	if err != nil {
		t.Fatalf("查询补交列表失败: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("应只有 1 条补交，实际 %d", len(list))
	}

	var events int
	if err := st.DB().QueryRow(
		`SELECT COUNT(1) FROM events WHERE assignment_id = 'HW1' AND type = 'extension_granted'`).Scan(&events); err != nil {
		t.Fatalf("统计事件失败: %v", err)
	}
	if events != 1 {
		t.Fatalf("应写 1 条补交事件，实际 %d", events)
	}

	// 同一学生同一作业第二次写入：主键拦截，且事务回滚不留事件
	err = st.CreateExtension(ctx, store.CreateExtensionParams{
		Extension: sampleExtension("S01"),
		State:     store.StateNotSubmitted,
		ActorRole: store.RoleTeacher,
	})
	if err == nil || !store.IsUniqueViolation(err) {
		t.Fatalf("重复补交应触发唯一约束，实际 %v", err)
	}
	if err := st.DB().QueryRow(
		`SELECT COUNT(1) FROM events WHERE assignment_id = 'HW1' AND type = 'extension_granted'`).Scan(&events); err != nil {
		t.Fatalf("统计事件失败: %v", err)
	}
	if events != 1 {
		t.Fatalf("失败写入不应留下事件，实际 %d 条", events)
	}
}
