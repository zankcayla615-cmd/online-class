package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"onlineclass/internal/store"
)

// seedPublishedWithEnrollments 写入一份已发布作业，并为班级学生建状态行。
func seedPublishedWithEnrollments(t *testing.T, st *store.Store, id string) {
	t.Helper()
	ts := time.Date(2026, 12, 1, 10, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	if _, err := st.DB().Exec(
		`INSERT INTO assignments
		 (id, class_id, teacher_id, title, requirement, deadline_at, state,
		  published_at, snapshot_title, snapshot_requirement, snapshot_deadline_at, snapshot_by, snapshot_at, created_at)
		 VALUES (?, 'P01', 'T01', '标题', '要求', ?, 'PUBLISHED', ?, '标题', '要求', ?, 'T01', ?, ?)`,
		id, ts, ts, ts, ts, ts); err != nil {
		t.Fatalf("写入已发布作业失败: %v", err)
	}
	if _, err := st.DB().Exec(
		`INSERT INTO enrollments (assignment_id, student_id, state, state_version, current_version_no)
		 SELECT ?, id, 'NOT_SUBMITTED', 0, 0 FROM users WHERE class_id = 'P01' AND role = 'student'`, id); err != nil {
		t.Fatalf("写入状态行失败: %v", err)
	}
}

func sampleSubmission(id string, version int) store.Submission {
	return store.Submission{
		ID:           id,
		AssignmentID: "HW1",
		StudentID:    "S01",
		VersionNo:    version,
		Content:      "第 " + string(rune('0'+version)) + " 版",
		ContentHash:  "hash-" + id,
		RequestID:    "req-" + id,
		CreatedAt:    time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC),
	}
}

func TestSubmitVersionAdvancesEnrollment(t *testing.T) {
	st := openTestStore(t)
	mustMigrateSeed(t, st)
	ctx := context.Background()
	seedPublishedWithEnrollments(t, st, "HW1")

	sub := sampleSubmission("SUB1", 1)
	err := st.SubmitVersion(ctx, store.SubmitVersionParams{
		Submission: sub, FromState: store.StateNotSubmitted, ToState: store.StateSubmitted,
		ExpectedVersionNo: 0, ActorRole: store.RoleStudent,
	})
	if err != nil {
		t.Fatalf("提交失败: %v", err)
	}

	enr, err := st.GetEnrollment(ctx, "HW1", "S01")
	if err != nil {
		t.Fatalf("读取状态行失败: %v", err)
	}
	if enr.State != store.StateSubmitted || enr.CurrentVersionNo != 1 || enr.StateVersion != 1 {
		t.Fatalf("状态行未推进: %+v", enr)
	}

	versions, err := st.ListSubmissions(ctx, "HW1", "S01")
	if err != nil {
		t.Fatalf("查询版本失败: %v", err)
	}
	if len(versions) != 1 || versions[0].ID != "SUB1" || versions[0].VersionNo != 1 {
		t.Fatalf("版本记录不符: %+v", versions)
	}

	var events int
	if err := st.DB().QueryRow(
		`SELECT COUNT(1) FROM events WHERE assignment_id = 'HW1' AND type = 'submitted'`).Scan(&events); err != nil {
		t.Fatalf("统计事件失败: %v", err)
	}
	if events != 1 {
		t.Fatalf("应写 1 条事件，实际 %d", events)
	}
}

func TestSubmitVersionRejectsStaleExpectation(t *testing.T) {
	st := openTestStore(t)
	mustMigrateSeed(t, st)
	ctx := context.Background()
	seedPublishedWithEnrollments(t, st, "HW1")

	// 期望版本号写错：模拟并发下别人已推进过状态行
	err := st.SubmitVersion(ctx, store.SubmitVersionParams{
		Submission: sampleSubmission("SUB1", 1), FromState: store.StateNotSubmitted, ToState: store.StateSubmitted,
		ExpectedVersionNo: 3, ActorRole: store.RoleStudent,
	})
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("期望版本不匹配应返回 ErrConflict，实际 %v", err)
	}

	// 事务回滚：版本行不应残留
	versions, err := st.ListSubmissions(ctx, "HW1", "S01")
	if err != nil {
		t.Fatalf("查询版本失败: %v", err)
	}
	if len(versions) != 0 {
		t.Fatalf("冲突时不得写入版本，实际 %d", len(versions))
	}
}

func TestSubmitVersionUniqueConstraints(t *testing.T) {
	st := openTestStore(t)
	mustMigrateSeed(t, st)
	ctx := context.Background()
	seedPublishedWithEnrollments(t, st, "HW1")

	first := sampleSubmission("SUB1", 1)
	if err := st.SubmitVersion(ctx, store.SubmitVersionParams{
		Submission: first, FromState: store.StateNotSubmitted, ToState: store.StateSubmitted,
		ExpectedVersionNo: 0, ActorRole: store.RoleStudent,
	}); err != nil {
		t.Fatalf("首次提交失败: %v", err)
	}

	// 同版本号不同请求号 → 唯一约束
	dup := sampleSubmission("SUB2", 1)
	err := st.SubmitVersion(ctx, store.SubmitVersionParams{
		Submission: dup, FromState: store.StateSubmitted, ToState: store.StateSubmitted,
		ExpectedVersionNo: 1, ActorRole: store.RoleStudent,
	})
	if err == nil || !store.IsUniqueViolation(err) {
		t.Fatalf("重复版本号应触发唯一约束，实际 %v", err)
	}

	// 同请求号不同版本号 → 唯一约束
	dupReq := sampleSubmission("SUB3", 2)
	dupReq.RequestID = first.RequestID
	err = st.SubmitVersion(ctx, store.SubmitVersionParams{
		Submission: dupReq, FromState: store.StateSubmitted, ToState: store.StateSubmitted,
		ExpectedVersionNo: 1, ActorRole: store.RoleStudent,
	})
	if err == nil || !store.IsUniqueViolation(err) {
		t.Fatalf("重复请求号应触发唯一约束，实际 %v", err)
	}
}

func TestGetEnrollmentAndExtensionNotFound(t *testing.T) {
	st := openTestStore(t)
	mustMigrateSeed(t, st)
	ctx := context.Background()

	if _, err := st.GetEnrollment(ctx, "NOPE", "S01"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("状态行不存在应返回 ErrNotFound，实际 %v", err)
	}
	if _, err := st.GetExtension(ctx, "NOPE", "S01"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("补交授权不存在应返回 ErrNotFound，实际 %v", err)
	}

	seedPublishedWithEnrollments(t, st, "HW1")
	if _, err := st.DB().Exec(
		`INSERT INTO extensions (assignment_id, student_id, deadline_at, reason, granted_by, granted_at)
		 VALUES ('HW1', 'S01', '2026-12-05T10:00:00Z', '生病', 'T01', '2026-11-13T09:00:00Z')`); err != nil {
		t.Fatalf("写入补交失败: %v", err)
	}
	ext, err := st.GetExtension(ctx, "HW1", "S01")
	if err != nil {
		t.Fatalf("读取补交失败: %v", err)
	}
	if ext.Reason != "生病" || ext.GrantedBy != "T01" {
		t.Fatalf("补交记录不符: %+v", ext)
	}
	if !ext.DeadlineAt.Equal(time.Date(2026, 12, 5, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("补交截止时间解析错误: %v", ext.DeadlineAt)
	}
}

func TestGetSubmissionByRequestID(t *testing.T) {
	st := openTestStore(t)
	mustMigrateSeed(t, st)
	ctx := context.Background()
	seedPublishedWithEnrollments(t, st, "HW1")

	if _, err := st.GetSubmissionByRequestID(ctx, "HW1", "S01", "nope"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("不存在请求号应返回 ErrNotFound，实际 %v", err)
	}

	sub := sampleSubmission("SUB1", 1)
	if err := st.SubmitVersion(ctx, store.SubmitVersionParams{
		Submission: sub, FromState: store.StateNotSubmitted, ToState: store.StateSubmitted,
		ExpectedVersionNo: 0, ActorRole: store.RoleStudent,
	}); err != nil {
		t.Fatalf("提交失败: %v", err)
	}
	got, err := st.GetSubmissionByRequestID(ctx, "HW1", "S01", sub.RequestID)
	if err != nil {
		t.Fatalf("查询请求号失败: %v", err)
	}
	if got.ID != sub.ID || got.VersionNo != 1 {
		t.Fatalf("请求号查询结果不符: %+v", got)
	}
}
