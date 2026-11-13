package service_test

import (
	"context"
	"testing"

	"onlineclass/internal/service"
	"onlineclass/internal/store"
)

func reviewInput(versionID, decision, feedback string) service.ReviewInput {
	return service.ReviewInput{VersionID: versionID, Decision: decision, Feedback: feedback}
}

// TestReviewPassUpdatesState 通过批阅：状态 PASSED、结论落库、事件写入。
func TestReviewPassUpdatesState(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	a := e.createPublished(t)
	subs := service.NewSubmissionService(e.st, e.clk)
	revs := service.NewReviewService(e.st, e.clk)
	teacher := e.user(t, "T01")

	sub, err := subs.Submit(ctx, e.user(t, "S01"), a.ID, submitInput("答案", "req-1"))
	if err != nil {
		t.Fatalf("提交失败: %v", err)
	}

	res, err := revs.Review(ctx, teacher, a.ID, reviewInput(sub.Version.ID, store.ReviewPass, "写得很清楚"))
	if err != nil {
		t.Fatalf("批阅失败: %v", err)
	}
	if res.Enrollment.State != store.StatePassed {
		t.Fatalf("通过后状态应为 PASSED，实际 %s", res.Enrollment.State)
	}
	if res.Review.VersionID != sub.Version.ID || res.Review.Decision != store.ReviewPass {
		t.Fatalf("结论未绑定版本: %+v", res.Review)
	}

	enr, err := e.st.GetEnrollment(ctx, a.ID, "S01")
	if err != nil {
		t.Fatalf("读取状态行失败: %v", err)
	}
	if enr.State != store.StatePassed || enr.StateVersion != 2 || enr.CurrentVersionNo != 1 {
		t.Fatalf("状态行未按预期推进: %+v", enr)
	}

	reviews, err := e.st.ListReviews(ctx, a.ID, "S01")
	if err != nil {
		t.Fatalf("查询批阅失败: %v", err)
	}
	if len(reviews) != 1 || reviews[0].TeacherID != "T01" || reviews[0].Feedback != "写得很清楚" {
		t.Fatalf("批阅记录不符: %+v", reviews)
	}

	var events int
	if err := e.st.DB().QueryRow(
		`SELECT COUNT(1) FROM events WHERE assignment_id = ? AND type = 'reviewed'`, a.ID).Scan(&events); err != nil {
		t.Fatalf("统计事件失败: %v", err)
	}
	if events != 1 {
		t.Fatalf("应写入 1 条批阅事件，实际 %d", events)
	}
}

// TestReviewRejectNeedsFeedback 退回必须给原因；通过可以不写反馈。
func TestReviewRejectNeedsFeedback(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	a := e.createPublished(t)
	subs := service.NewSubmissionService(e.st, e.clk)
	revs := service.NewReviewService(e.st, e.clk)
	teacher := e.user(t, "T01")

	sub, err := subs.Submit(ctx, e.user(t, "S01"), a.ID, submitInput("答案", "req-1"))
	if err != nil {
		t.Fatalf("提交失败: %v", err)
	}

	if _, err := revs.Review(ctx, teacher, a.ID, reviewInput(sub.Version.ID, store.ReviewReject, "   ")); err == nil || code(t, err) != "invalid_input" {
		t.Fatalf("退回缺少反馈应 400，实际 %v", err)
	}

	res, err := revs.Review(ctx, teacher, a.ID, reviewInput(sub.Version.ID, store.ReviewReject, "缺少单元测试"))
	if err != nil {
		t.Fatalf("退回失败: %v", err)
	}
	if res.Enrollment.State != store.StateReturned {
		t.Fatalf("退回后状态应为 RETURNED，实际 %s", res.Enrollment.State)
	}
}

// TestReviewRejectsNonCurrentVersion 旧版本不能再批，且不改变当前状态。
func TestReviewRejectsNonCurrentVersion(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	a := e.createPublished(t)
	subs := service.NewSubmissionService(e.st, e.clk)
	revs := service.NewReviewService(e.st, e.clk)
	s01 := e.user(t, "S01")

	v1, err := subs.Submit(ctx, s01, a.ID, submitAt("第一版", "req-1", 0))
	if err != nil {
		t.Fatalf("首次提交失败: %v", err)
	}
	if _, err := subs.Submit(ctx, s01, a.ID, submitAt("第二版", "req-2", 1)); err != nil {
		t.Fatalf("更新失败: %v", err)
	}

	_, err = revs.Review(ctx, e.user(t, "T01"), a.ID, reviewInput(v1.Version.ID, store.ReviewPass, ""))
	if err == nil || code(t, err) != "stale_version" {
		t.Fatalf("批阅旧版本应返回 stale_version，实际 %v", err)
	}
	reviews, err := e.st.ListReviews(ctx, a.ID, "S01")
	if err != nil {
		t.Fatalf("查询批阅失败: %v", err)
	}
	if len(reviews) != 0 {
		t.Fatalf("被拒批阅不得落库，实际 %d 条", len(reviews))
	}
	enr, err := e.st.GetEnrollment(ctx, a.ID, "S01")
	if err != nil {
		t.Fatalf("读取状态行失败: %v", err)
	}
	if enr.State != store.StateSubmitted || enr.CurrentVersionNo != 2 {
		t.Fatalf("状态不应被旧版本批阅改变: %+v", enr)
	}
}

// TestReviewIdempotentAndConflicting 同版本同结论幂等，异结论 409。
func TestReviewIdempotentAndConflicting(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	a := e.createPublished(t)
	subs := service.NewSubmissionService(e.st, e.clk)
	revs := service.NewReviewService(e.st, e.clk)
	teacher := e.user(t, "T01")

	sub, err := subs.Submit(ctx, e.user(t, "S01"), a.ID, submitInput("答案", "req-1"))
	if err != nil {
		t.Fatalf("提交失败: %v", err)
	}
	first, err := revs.Review(ctx, teacher, a.ID, reviewInput(sub.Version.ID, store.ReviewPass, "很好"))
	if err != nil {
		t.Fatalf("首次批阅失败: %v", err)
	}

	// 同版本同结论 → 重放，不新增记录、不再推进 state_version
	replay, err := revs.Review(ctx, teacher, a.ID, reviewInput(sub.Version.ID, store.ReviewPass, "很好"))
	if err != nil {
		t.Fatalf("同结论重放应成功，实际 %v", err)
	}
	if !replay.Replayed || replay.Review.ID != first.Review.ID {
		t.Fatalf("应返回首次结论并标记重放，实际 %+v", replay)
	}

	// 同版本异结论 → 409
	_, err = revs.Review(ctx, teacher, a.ID, reviewInput(sub.Version.ID, store.ReviewReject, "改判"))
	if err == nil || code(t, err) != "review_conflict" {
		t.Fatalf("同版本异结论应返回 review_conflict，实际 %v", err)
	}

	reviews, err := e.st.ListReviews(ctx, a.ID, "S01")
	if err != nil {
		t.Fatalf("查询批阅失败: %v", err)
	}
	if len(reviews) != 1 {
		t.Fatalf("一版只能有一个结论，实际 %d", len(reviews))
	}
	enr, err := e.st.GetEnrollment(ctx, a.ID, "S01")
	if err != nil {
		t.Fatalf("读取状态行失败: %v", err)
	}
	if enr.StateVersion != 2 {
		t.Fatalf("重放与冲突都不得再推进状态，state_version=%d", enr.StateVersion)
	}
}

// TestReviewAfterResubmitKeepsOldReview 退回后重交，旧版本与旧意见仍可查。
func TestReviewAfterResubmitKeepsOldReview(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	a := e.createPublished(t)
	subs := service.NewSubmissionService(e.st, e.clk)
	revs := service.NewReviewService(e.st, e.clk)
	teacher := e.user(t, "T01")
	s01 := e.user(t, "S01")

	v1, err := subs.Submit(ctx, s01, a.ID, submitAt("第一版", "req-1", 0))
	if err != nil {
		t.Fatalf("首次提交失败: %v", err)
	}
	if _, err := revs.Review(ctx, teacher, a.ID, reviewInput(v1.Version.ID, store.ReviewReject, "补测试")); err != nil {
		t.Fatalf("退回失败: %v", err)
	}
	v2, err := subs.Submit(ctx, s01, a.ID, submitAt("第二版", "req-2", 1))
	if err != nil {
		t.Fatalf("退回后重交失败: %v", err)
	}
	if _, err := revs.Review(ctx, teacher, a.ID, reviewInput(v2.Version.ID, store.ReviewPass, "通过")); err != nil {
		t.Fatalf("批阅第二版失败: %v", err)
	}

	h, err := subs.History(ctx, s01, a.ID, "")
	if err != nil {
		t.Fatalf("查询历史失败: %v", err)
	}
	if len(h.Versions) != 2 {
		t.Fatalf("应保留两版，实际 %d", len(h.Versions))
	}
	if rev, ok := h.Reviews[1]; !ok || rev.Decision != store.ReviewReject || rev.Feedback != "补测试" {
		t.Fatalf("旧版本意见应可查: %+v", h.Reviews)
	}
	if rev, ok := h.Reviews[2]; !ok || rev.Decision != store.ReviewPass {
		t.Fatalf("新版本意见应可查: %+v", h.Reviews)
	}
	if h.State != store.StatePassed || h.CurrentVersionNo != 2 {
		t.Fatalf("最终状态不符: %+v", h)
	}
}

// TestReviewPermissionAndValidation 角色/班级/参数校验。
func TestReviewPermissionAndValidation(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	a := e.createPublished(t)
	subs := service.NewSubmissionService(e.st, e.clk)
	revs := service.NewReviewService(e.st, e.clk)

	sub, err := subs.Submit(ctx, e.user(t, "S01"), a.ID, submitInput("答案", "req-1"))
	if err != nil {
		t.Fatalf("提交失败: %v", err)
	}

	for _, id := range []string{"A01", "S01"} {
		if _, err := revs.Review(ctx, e.user(t, id), a.ID, reviewInput(sub.Version.ID, store.ReviewPass, "")); err == nil || code(t, err) != "forbidden_role" {
			t.Fatalf("%s 批阅应被拒绝，实际 %v", id, err)
		}
	}

	// 外班教师（服务层只认班级，不需要账号存在）
	outsider := store.User{ID: "T99", ClassID: "P02", Role: store.RoleTeacher}
	if _, err := revs.Review(ctx, outsider, a.ID, reviewInput(sub.Version.ID, store.ReviewPass, "")); err == nil || code(t, err) != "not_found" {
		t.Fatalf("外班教师批阅应 404，实际 %v", err)
	}

	// 草稿不可批阅
	draft, err := e.svc.Create(ctx, e.user(t, "T01"), validInput(e))
	if err != nil {
		t.Fatalf("创建草稿失败: %v", err)
	}
	if _, err := revs.Review(ctx, e.user(t, "T01"), draft.ID, reviewInput(sub.Version.ID, store.ReviewPass, "")); err == nil || code(t, err) != "not_published" {
		t.Fatalf("草稿批阅应 409 not_published，实际 %v", err)
	}

	cases := []struct {
		name string
		in   service.ReviewInput
	}{
		{"缺少版本号", service.ReviewInput{Decision: store.ReviewPass}},
		{"非法结论", reviewInput(sub.Version.ID, "maybe", "")},
		{"不存在的版本", reviewInput("SUB-NOPE", store.ReviewPass, "")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := revs.Review(ctx, e.user(t, "T01"), a.ID, c.in)
			want := "invalid_input"
			if c.name == "不存在的版本" {
				want = "not_found"
			}
			if err == nil || code(t, err) != want {
				t.Fatalf("%s 应返回 %s，实际 %v", c.name, want, err)
			}
		})
	}
}
