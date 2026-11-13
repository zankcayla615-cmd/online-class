package store_test

import (
	"context"
	"testing"
	"time"

	"onlineclass/internal/store"
)

// TestListEnrollmentRowsOmitsContent 状态行快照只带状态与时间，不带正文。
func TestListEnrollmentRowsOmitsContent(t *testing.T) {
	st := openTestStore(t)
	mustMigrateSeed(t, st)
	ctx := context.Background()
	seedPublishedWithEnrollments(t, st, "HW1")

	rows, err := st.ListEnrollmentRows(ctx, "HW1")
	if err != nil {
		t.Fatalf("查询状态行失败: %v", err)
	}
	if len(rows) != 5 {
		t.Fatalf("应有 5 名学生，实际 %d", len(rows))
	}
	for _, row := range rows {
		if row.StudentName == "" {
			t.Fatalf("缺少姓名: %+v", row)
		}
		if row.SubmissionCount != 0 || !row.FirstSubmittedAt.IsZero() {
			t.Fatalf("未提交的学生不应有提交时间: %+v", row)
		}
	}
}

// TestListEventsAndReminders 事件流与催交记录都按时间正序返回。
func TestListEventsAndReminders(t *testing.T) {
	st := openTestStore(t)
	mustMigrateSeed(t, st)
	ctx := context.Background()
	seedSubmitted(t, st) // 内部已建作业与状态行，并写入一条提交事件

	events, err := st.ListEvents(ctx, "HW1", "")
	if err != nil {
		t.Fatalf("查询事件失败: %v", err)
	}
	if len(events) == 0 || events[0].Type != "submitted" {
		t.Fatalf("首个事件应为提交，实际 %+v", events)
	}
	scoped, err := st.ListEvents(ctx, "HW1", "S02")
	if err != nil {
		t.Fatalf("按学生查询事件失败: %v", err)
	}
	if len(scoped) != 0 {
		t.Fatalf("S02 没有事件，实际 %d 条", len(scoped))
	}

	base := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	for i, note := range []string{"第一次电话", "第二次群里提醒"} {
		if err := st.CreateReminder(ctx, store.Reminder{
			ID:           "RM-" + string(rune('1'+i)),
			AssignmentID: "HW1",
			StudentID:    "S01",
			AssistantID:  "A01",
			Channel:      store.ChannelPhone,
			Note:         note,
			CreatedAt:    base.Add(time.Duration(i) * time.Hour),
		}); err != nil {
			t.Fatalf("写入催交失败: %v", err)
		}
	}

	reminders, err := st.ListReminders(ctx, "HW1")
	if err != nil {
		t.Fatalf("查询催交失败: %v", err)
	}
	if len(reminders) != 2 || reminders[0].Note != "第一次电话" || reminders[1].Note != "第二次群里提醒" {
		t.Fatalf("催交记录应按时间正序保留两条，实际 %+v", reminders)
	}
}
