package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"onlineclass/internal/store"
)

func seedSubmitted(t *testing.T, st *store.Store) store.Submission {
	t.Helper()
	ctx := context.Background()
	seedPublishedWithEnrollments(t, st, "HW1")
	sub := sampleSubmission("SUB1", 1)
	if err := st.SubmitVersion(ctx, store.SubmitVersionParams{
		Submission: sub, FromState: store.StateNotSubmitted, ToState: store.StateSubmitted,
		ExpectedVersionNo: 0, ActorRole: store.RoleStudent,
	}); err != nil {
		t.Fatalf("准备提交失败: %v", err)
	}
	return sub
}

func sampleReview(versionID, decision string) store.Review {
	return store.Review{
		ID: "RV1", VersionID: versionID, Decision: decision,
		Feedback: "看起来不错", TeacherID: "T01",
		CreatedAt: time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC),
	}
}

func TestCreateReviewAdvancesEnrollmentAndEvent(t *testing.T) {
	st := openTestStore(t)
	mustMigrateSeed(t, st)
	ctx := context.Background()
	sub := seedSubmitted(t, st)

	_, err := st.CreateReview(ctx, store.CreateReviewParams{
		Review: sampleReview(sub.ID, store.ReviewPass), AssignmentID: "HW1", StudentID: "S01",
		FromState: store.StateSubmitted, ToState: store.StatePassed,
		ExpectedVersionNo: 1, ActorRole: store.RoleTeacher,
	})
	if err != nil {
		t.Fatalf("批阅失败: %v", err)
	}

	enr, err := st.GetEnrollment(ctx, "HW1", "S01")
	if err != nil {
		t.Fatalf("读取状态行失败: %v", err)
	}
	if enr.State != store.StatePassed || enr.StateVersion != 2 {
		t.Fatalf("状态行未推进: %+v", enr)
	}

	rev, err := st.GetReviewByVersionID(ctx, sub.ID)
	if err != nil {
		t.Fatalf("读取批阅失败: %v", err)
	}
	if rev.Decision != store.ReviewPass || rev.TeacherID != "T01" {
		t.Fatalf("批阅记录不符: %+v", rev)
	}

	var events int
	if err := st.DB().QueryRow(
		`SELECT COUNT(1) FROM events WHERE assignment_id = 'HW1' AND type = 'reviewed'`).Scan(&events); err != nil {
		t.Fatalf("统计事件失败: %v", err)
	}
	if events != 1 {
		t.Fatalf("应写 1 条批阅事件，实际 %d", events)
	}
}

func TestCreateReviewRejectsDuplicateVersion(t *testing.T) {
	st := openTestStore(t)
	mustMigrateSeed(t, st)
	ctx := context.Background()
	sub := seedSubmitted(t, st)

	params := store.CreateReviewParams{
		Review: sampleReview(sub.ID, store.ReviewPass), AssignmentID: "HW1", StudentID: "S01",
		FromState: store.StateSubmitted, ToState: store.StatePassed,
		ExpectedVersionNo: 1, ActorRole: store.RoleTeacher,
	}
	if _, err := st.CreateReview(ctx, params); err != nil {
		t.Fatalf("首次批阅失败: %v", err)
	}

	// 同一版本第二次结论：UNIQUE(version_id) 拦截
	params.Review = sampleReview(sub.ID, store.ReviewReject)
	_, err := st.CreateReview(ctx, params)
	if err == nil || !store.IsUniqueViolation(err) {
		t.Fatalf("同版本第二结论应触发唯一约束，实际 %v", err)
	}

	// 事务回滚：结论仍只有一条，状态未被改判
	reviews, err := st.ListReviews(ctx, "HW1", "S01")
	if err != nil {
		t.Fatalf("查询批阅失败: %v", err)
	}
	if len(reviews) != 1 || reviews[0].Decision != store.ReviewPass {
		t.Fatalf("回滚后结论应保持首次结果，实际 %+v", reviews)
	}
}

func TestCreateReviewStaleStateConflict(t *testing.T) {
	st := openTestStore(t)
	mustMigrateSeed(t, st)
	ctx := context.Background()
	sub := seedSubmitted(t, st)

	// 期望版本号写错 → 条件更新未命中 → ErrConflict，且不写入结论
	_, err := st.CreateReview(ctx, store.CreateReviewParams{
		Review: sampleReview(sub.ID, store.ReviewPass), AssignmentID: "HW1", StudentID: "S01",
		FromState: store.StateSubmitted, ToState: store.StatePassed,
		ExpectedVersionNo: 9, ActorRole: store.RoleTeacher,
	})
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("期望版本不匹配应返回 ErrConflict，实际 %v", err)
	}
	if _, err := st.GetReviewByVersionID(ctx, sub.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("冲突时不得写入结论，实际 %v", err)
	}
}

func TestGetSubmissionByVersionIDScopedByAssignment(t *testing.T) {
	st := openTestStore(t)
	mustMigrateSeed(t, st)
	ctx := context.Background()
	sub := seedSubmitted(t, st)

	if _, err := st.GetSubmissionByVersionID(ctx, "HW1", sub.ID); err != nil {
		t.Fatalf("同作业查询应成功: %v", err)
	}
	if _, err := st.GetSubmissionByVersionID(ctx, "HW-OTHER", sub.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("跨作业查询应 ErrNotFound，实际 %v", err)
	}
	if _, err := st.GetReviewByVersionID(ctx, sub.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("未批阅时应 ErrNotFound，实际 %v", err)
	}
}
