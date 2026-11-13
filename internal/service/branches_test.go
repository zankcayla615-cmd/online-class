package service_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"onlineclass/internal/service"
	"onlineclass/internal/store"
)

// TestAssignmentNotFoundBranches 不存在与越权路径不能落到"写入成功"。
func TestAssignmentNotFoundBranches(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	teacher := e.user(t, "T01")
	assistant := e.user(t, "A01")

	in := validInput(e)
	if _, err := e.svc.UpdateDraft(ctx, teacher, "HW-NOPE", in); err == nil || code(t, err) != "not_found" {
		t.Fatalf("修改不存在作业应 404，实际 %v", err)
	}
	if _, err := e.svc.Publish(ctx, teacher, "HW-NOPE"); err == nil || code(t, err) != "not_found" {
		t.Fatalf("发布不存在作业应 404，实际 %v", err)
	}
	if _, err := e.svc.Publish(ctx, assistant, "HW-NOPE"); err == nil || code(t, err) != "forbidden_role" {
		t.Fatalf("助教发布应 403（先判角色），实际 %v", err)
	}
	if _, err := e.svc.UpdateDraft(ctx, assistant, "HW-NOPE", in); err == nil || code(t, err) != "forbidden_role" {
		t.Fatalf("助教修改应 403，实际 %v", err)
	}

	a := e.createPublished(t)
	if _, err := e.svc.Publish(ctx, teacher, a.ID); err == nil || code(t, err) != "already_published" {
		t.Fatalf("重复发布应 409，实际 %v", err)
	}

	bad := in
	bad.Title = " "
	if _, err := e.svc.UpdateDraft(ctx, teacher, a.ID, bad); err == nil || code(t, err) != "assignment_locked" {
		t.Fatalf("已发布作业先判锁定（即使参数非法），实际 %v", err)
	}
	draft, err := e.svc.Create(ctx, teacher, in)
	if err != nil {
		t.Fatalf("创建草稿失败: %v", err)
	}
	if _, err := e.svc.UpdateDraft(ctx, teacher, draft.ID, bad); err == nil || code(t, err) != "invalid_input" {
		t.Fatalf("草稿参数非法应 400，实际 %v", err)
	}
}

// TestBoardOnDraftVisibility 草稿只有教师本人能看到"未发布"，助教按不存在处理。
func TestBoardOnDraftVisibility(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	overview := service.NewOverviewService(e.st, e.clk)
	draft, err := e.svc.Create(ctx, e.user(t, "T01"), validInput(e))
	if err != nil {
		t.Fatalf("创建草稿失败: %v", err)
	}

	if _, err := overview.Board(ctx, e.user(t, "T01"), draft.ID); err == nil || code(t, err) != "not_published" {
		t.Fatalf("教师看草稿看板应 409，实际 %v", err)
	}
	if _, err := overview.Board(ctx, e.user(t, "A01"), draft.ID); err == nil || code(t, err) != "not_found" {
		t.Fatalf("助教看草稿应 404，实际 %v", err)
	}
	if _, err := overview.Summary(ctx, e.user(t, "A01"), draft.ID); err == nil || code(t, err) != "not_found" {
		t.Fatalf("助教看草稿汇总应 404，实际 %v", err)
	}
}

// TestHistoryEnrollmentAndLookupErrors 未入班与未知学生都要给出明确错误。
func TestHistoryEnrollmentAndLookupErrors(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	a := e.createPublished(t)
	subs := service.NewSubmissionService(e.st, e.clk)

	if _, err := e.st.DB().Exec(`DELETE FROM enrollments WHERE assignment_id = ? AND student_id = 'S05'`, a.ID); err != nil {
		t.Fatalf("删除状态行失败: %v", err)
	}
	if _, err := subs.History(ctx, e.user(t, "S05"), a.ID, ""); err == nil || code(t, err) != "not_found" {
		t.Fatalf("未入班学生查历史应按不存在处理，实际 %v", err)
	}
	if _, err := subs.History(ctx, e.user(t, "T01"), a.ID, "S99"); err == nil || code(t, err) != "not_found" {
		t.Fatalf("未知学生应 404，实际 %v", err)
	}
}

// TestGrantMoreValidation 已通过、非学生、未入班、原因超长。
func TestGrantMoreValidation(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	a := e.createPublished(t)
	subs := service.NewSubmissionService(e.st, e.clk)
	revs := service.NewReviewService(e.st, e.clk)
	exts := service.NewExtensionService(e.st, e.clk)
	teacher := e.user(t, "T01")
	future := e.clk.Now().Add(48 * time.Hour)

	sub, err := subs.Submit(ctx, e.user(t, "S01"), a.ID, submitInput("答案", "r1"))
	if err != nil {
		t.Fatalf("提交失败: %v", err)
	}
	if _, err := revs.Review(ctx, teacher, a.ID, reviewInput(sub.Version.ID, store.ReviewPass, "通过")); err != nil {
		t.Fatalf("批阅失败: %v", err)
	}
	if _, err := exts.Grant(ctx, teacher, a.ID, extInput("S01", future, "已通过还想补交")); err == nil || code(t, err) != "already_passed" {
		t.Fatalf("已通过学生不应再批补交，实际 %v", err)
	}

	if _, err := exts.Grant(ctx, teacher, a.ID, extInput("A01", future, "助教不是学生")); err == nil || code(t, err) != "not_found" {
		t.Fatalf("非学生账号应 404，实际 %v", err)
	}
	if _, err := exts.Grant(ctx, teacher, a.ID, extInput("X01", future, "外班学生不能补交")); err == nil || code(t, err) != "not_found" {
		t.Fatalf("外班学生应 404，实际 %v", err)
	}
	if _, err := e.st.DB().Exec(`DELETE FROM enrollments WHERE assignment_id = ? AND student_id = 'S05'`, a.ID); err != nil {
		t.Fatalf("删除状态行失败: %v", err)
	}
	if _, err := exts.Grant(ctx, teacher, a.ID, extInput("S05", future, "未入班不能补交")); err == nil || code(t, err) != "not_enrolled" {
		t.Fatalf("未入班学生应 409，实际 %v", err)
	}
	if _, err := exts.Grant(ctx, teacher, a.ID, extInput("S02", future, strings.Repeat("因", 201))); err == nil || code(t, err) != "invalid_input" {
		t.Fatalf("原因超长应 400，实际 %v", err)
	}
}

// TestReminderNotEnrolled 已发布但学生没有状态行时不能催交。
func TestReminderNotEnrolled(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	a := e.createPublished(t)
	overview := service.NewOverviewService(e.st, e.clk)
	assistant := e.user(t, "A01")

	if _, err := e.st.DB().Exec(`DELETE FROM enrollments WHERE assignment_id = ? AND student_id = 'S04'`, a.ID); err != nil {
		t.Fatalf("删除状态行失败: %v", err)
	}
	_, err := overview.RecordReminder(ctx, assistant, a.ID, service.ReminderInput{
		StudentID: "S04", Channel: store.ChannelPhone, Note: "电话未接",
	})
	if err == nil || code(t, err) != "not_enrolled" {
		t.Fatalf("未入班学生催交应 409，实际 %v", err)
	}
}
