package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"onlineclass/internal/store"
)

func sampleAward(studentID string) *store.PointsEntry {
	return &store.PointsEntry{
		ID:           "PT1",
		StudentID:    studentID,
		AssignmentID: "HW1",
		Reason:       store.ReasonFirstPass,
		Points:       store.PointsFirstPass,
		CreatedAt:    time.Date(2026, 11, 13, 10, 30, 0, 0, time.UTC),
	}
}

func ledgerCount(t *testing.T, st *store.Store, studentID string) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRow(
		`SELECT COUNT(1) FROM points_ledger WHERE student_id = ?`, studentID).Scan(&n); err != nil {
		t.Fatalf("统计积分流水失败: %v", err)
	}
	return n
}

// TestCreateReviewAwardsPointsInSameTx 通过批阅在同一事务内发 10 分。
func TestCreateReviewAwardsPointsInSameTx(t *testing.T) {
	st := openTestStore(t)
	mustMigrateSeed(t, st)
	ctx := context.Background()
	sub := seedSubmitted(t, st)

	out, err := st.CreateReview(ctx, store.CreateReviewParams{
		Review: sampleReview(sub.ID, store.ReviewPass), AssignmentID: "HW1", StudentID: "S01",
		FromState: store.StateSubmitted, ToState: store.StatePassed,
		ExpectedVersionNo: 1, ActorRole: store.RoleTeacher,
		Award: sampleAward("S01"),
	})
	if err != nil {
		t.Fatalf("批阅失败: %v", err)
	}
	if !out.Awarded {
		t.Fatal("首次通过应发奖")
	}

	entry, err := st.GetPointsEntry(ctx, "HW1", "S01", store.ReasonFirstPass)
	if err != nil {
		t.Fatalf("读取积分流水失败: %v", err)
	}
	if entry.Points != store.PointsFirstPass || entry.Reason != store.ReasonFirstPass {
		t.Fatalf("流水不符: %+v", entry)
	}
	total, err := st.TotalPoints(ctx, "S01")
	if err != nil {
		t.Fatalf("统计总分失败: %v", err)
	}
	if total != 10 {
		t.Fatalf("总分应为 10，实际 %d", total)
	}
	if n := ledgerCount(t, st, "S01"); n != 1 {
		t.Fatalf("应只有 1 条流水，实际 %d", n)
	}

	list, err := st.ListPointsByAssignment(ctx, "HW1")
	if err != nil {
		t.Fatalf("按作业查询流水失败: %v", err)
	}
	if len(list) != 1 || list[0].StudentID != "S01" {
		t.Fatalf("作业流水不符: %+v", list)
	}
}

// TestAwardIsIdempotentWhenLedgerExists 已存在流水时不重复发奖，但批阅照常成功。
func TestAwardIsIdempotentWhenLedgerExists(t *testing.T) {
	st := openTestStore(t)
	mustMigrateSeed(t, st)
	ctx := context.Background()
	sub := seedSubmitted(t, st)

	// 模拟"历史数据已发过奖"：先塞一条同键流水
	if _, err := st.DB().Exec(
		`INSERT INTO points_ledger (id, student_id, assignment_id, reason, points, created_at)
		 VALUES ('PT-OLD', 'S01', 'HW1', ?, ?, '2026-11-01T00:00:00Z')`,
		store.ReasonFirstPass, store.PointsFirstPass); err != nil {
		t.Fatalf("预置流水失败: %v", err)
	}

	out, err := st.CreateReview(ctx, store.CreateReviewParams{
		Review: sampleReview(sub.ID, store.ReviewPass), AssignmentID: "HW1", StudentID: "S01",
		FromState: store.StateSubmitted, ToState: store.StatePassed,
		ExpectedVersionNo: 1, ActorRole: store.RoleTeacher,
		Award: sampleAward("S01"),
	})
	if err != nil {
		t.Fatalf("批阅不应因唯一冲突失败: %v", err)
	}
	if out.Awarded {
		t.Fatal("已有流水时不应标记为本次发奖")
	}
	if n := ledgerCount(t, st, "S01"); n != 1 {
		t.Fatalf("流水仍应只有 1 条，实际 %d", n)
	}
	if total, _ := st.TotalPoints(ctx, "S01"); total != 10 {
		t.Fatalf("总分应保持 10，实际 %d", total)
	}
}

// TestAwardRollsBackWithReview 批阅失败时积分一并回滚，不存在"没批成却发了奖"。
func TestAwardRollsBackWithReview(t *testing.T) {
	st := openTestStore(t)
	mustMigrateSeed(t, st)
	ctx := context.Background()
	sub := seedSubmitted(t, st)

	_, err := st.CreateReview(ctx, store.CreateReviewParams{
		Review: sampleReview(sub.ID, store.ReviewPass), AssignmentID: "HW1", StudentID: "S01",
		FromState: store.StateSubmitted, ToState: store.StatePassed,
		ExpectedVersionNo: 9, ActorRole: store.RoleTeacher,
		Award: sampleAward("S01"),
	})
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("期望版本不匹配应返回 ErrConflict，实际 %v", err)
	}
	if n := ledgerCount(t, st, "S01"); n != 0 {
		t.Fatalf("批阅失败时不得留下流水，实际 %d 条", n)
	}
	if _, err := st.GetPointsEntry(ctx, "HW1", "S01", store.ReasonFirstPass); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("流水应不存在，实际 %v", err)
	}
}
