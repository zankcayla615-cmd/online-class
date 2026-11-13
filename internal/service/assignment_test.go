package service_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"onlineclass/internal/apperr"
	"onlineclass/internal/clock"
	"onlineclass/internal/service"
	"onlineclass/internal/store"
)

type svcEnv struct {
	st  *store.Store
	clk *clock.Clock
	svc *service.AssignmentService
}

func newSvcEnv(t *testing.T) *svcEnv {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	if err := st.Seed(); err != nil {
		t.Fatalf("种子失败: %v", err)
	}
	clk := clock.New(0)
	return &svcEnv{st: st, clk: clk, svc: service.NewAssignmentService(st, clk)}
}

func (e *svcEnv) user(t *testing.T, id string) store.User {
	t.Helper()
	u, err := e.st.GetUser(context.Background(), id)
	if err != nil {
		t.Fatalf("读取账号 %s 失败: %v", id, err)
	}
	return u
}

func validInput(e *svcEnv) service.AssignmentInput {
	return service.AssignmentInput{
		Title:       "第 3 章 并发编程",
		Requirement: "实现带超时的限流器，提交 main.go 与测试",
		DeadlineAt:  e.clk.Now().Add(72 * time.Hour),
	}
}

func code(t *testing.T, err error) string {
	t.Helper()
	var ae *apperr.Error
	if !errors.As(err, &ae) {
		t.Fatalf("应为结构化业务错误，实际 %v", err)
	}
	return ae.Code
}

func TestCreateDraft(t *testing.T) {
	e := newSvcEnv(t)
	teacher := e.user(t, "T01")

	before := e.clk.Now()
	a, err := e.svc.Create(context.Background(), teacher, validInput(e))
	after := e.clk.Now()
	if err != nil {
		t.Fatalf("创建草稿失败: %v", err)
	}
	if a.State != store.AssignmentDraft {
		t.Fatalf("新建作业应为草稿，实际 %s", a.State)
	}
	if a.ClassID != "P01" || a.TeacherID != "T01" {
		t.Fatalf("班级/作者应取自会话身份，实际 %s/%s", a.ClassID, a.TeacherID)
	}
	if !strings.HasPrefix(a.ID, "HW-") {
		t.Fatalf("作业编号格式异常: %s", a.ID)
	}
	if a.CreatedAt.Before(before) || a.CreatedAt.After(after) {
		t.Fatalf("创建时间应落在注入时钟区间内，实际 %v", a.CreatedAt)
	}
}

func TestCreateForbiddenForNonTeacher(t *testing.T) {
	e := newSvcEnv(t)
	for _, id := range []string{"A01", "S01"} {
		_, err := e.svc.Create(context.Background(), e.user(t, id), validInput(e))
		if err == nil || code(t, err) != "forbidden_role" {
			t.Fatalf("%s 创建作业应被拒绝，实际 %v", id, err)
		}
	}
}

func TestCreateValidation(t *testing.T) {
	e := newSvcEnv(t)
	teacher := e.user(t, "T01")

	cases := []struct {
		name string
		in   service.AssignmentInput
	}{
		{"空标题", service.AssignmentInput{Title: "  ", Requirement: "要求", DeadlineAt: e.clk.Now().Add(time.Hour)}},
		{"标题超长", service.AssignmentInput{Title: strings.Repeat("超", 101), Requirement: "要求", DeadlineAt: e.clk.Now().Add(time.Hour)}},
		{"空要求", service.AssignmentInput{Title: "标题", Requirement: " ", DeadlineAt: e.clk.Now().Add(time.Hour)}},
		{"要求超长", service.AssignmentInput{Title: "标题", Requirement: strings.Repeat("超", 2001), DeadlineAt: e.clk.Now().Add(time.Hour)}},
		{"截止为空", service.AssignmentInput{Title: "标题", Requirement: "要求"}},
		{"截止已过", service.AssignmentInput{Title: "标题", Requirement: "要求", DeadlineAt: e.clk.Now().Add(-time.Second)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := e.svc.Create(context.Background(), teacher, c.in); err == nil || code(t, err) != "invalid_input" {
				t.Fatalf("%s 应返回 invalid_input，实际 %v", c.name, err)
			}
		})
	}
}

func TestUpdateDraft(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	teacher := e.user(t, "T01")

	a, err := e.svc.Create(ctx, teacher, validInput(e))
	if err != nil {
		t.Fatalf("创建草稿失败: %v", err)
	}
	in := validInput(e)
	in.Title = "改后标题"
	in.Requirement = "改后要求"
	updated, err := e.svc.UpdateDraft(ctx, teacher, a.ID, in)
	if err != nil {
		t.Fatalf("修改草稿失败: %v", err)
	}
	if updated.Title != "改后标题" || updated.Requirement != "改后要求" {
		t.Fatalf("草稿内容未生效: %s / %s", updated.Title, updated.Requirement)
	}
}

func TestUpdateAfterPublishIsLocked(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	teacher := e.user(t, "T01")

	a, err := e.svc.Create(ctx, teacher, validInput(e))
	if err != nil {
		t.Fatalf("创建草稿失败: %v", err)
	}
	published, err := e.svc.Publish(ctx, teacher, a.ID)
	if err != nil {
		t.Fatalf("发布失败: %v", err)
	}

	in := validInput(e)
	in.Title = "偷偷改标题"
	in.Requirement = "偷偷改要求"
	if _, err := e.svc.UpdateDraft(ctx, teacher, a.ID, in); err == nil || code(t, err) != "assignment_locked" {
		t.Fatalf("发布后修改应返回 assignment_locked，实际 %v", err)
	}

	reloaded, err := e.st.GetAssignment(ctx, a.ID)
	if err != nil {
		t.Fatalf("重新读取失败: %v", err)
	}
	if reloaded.Title != published.Title || reloaded.Requirement != published.Requirement {
		t.Fatalf("被拒修改不得改变内容，实际 %s / %s", reloaded.Title, reloaded.Requirement)
	}
	if !reloaded.SnapshotAt.Equal(published.SnapshotAt) {
		t.Fatal("被拒修改不得改变快照时间")
	}
}

func TestPublishCreatesSnapshotAndEnrollments(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	teacher := e.user(t, "T01")

	in := validInput(e)
	a, err := e.svc.Create(ctx, teacher, in)
	if err != nil {
		t.Fatalf("创建草稿失败: %v", err)
	}
	before := e.clk.Now()
	published, err := e.svc.Publish(ctx, teacher, a.ID)
	after := e.clk.Now()
	if err != nil {
		t.Fatalf("发布失败: %v", err)
	}

	if !published.Published() {
		t.Fatalf("发布后状态应为 PUBLISHED，实际 %s", published.State)
	}
	if published.SnapshotTitle != strings.TrimSpace(in.Title) ||
		published.SnapshotRequirement != strings.TrimSpace(in.Requirement) {
		t.Fatalf("快照应逐字等于发布时内容，实际 %s / %s", published.SnapshotTitle, published.SnapshotRequirement)
	}
	if !published.SnapshotDeadlineAt.Equal(in.DeadlineAt.UTC()) {
		t.Fatalf("快照截止时间不一致: %v vs %v", published.SnapshotDeadlineAt, in.DeadlineAt.UTC())
	}
	if published.SnapshotBy != "T01" {
		t.Fatalf("快照操作人错误: %s", published.SnapshotBy)
	}
	if published.SnapshotAt.Before(before) || published.SnapshotAt.After(after) {
		t.Fatalf("快照时间应取注入时钟且落在发布区间内，实际 %v", published.SnapshotAt)
	}
	if published.PublishedAt.Before(before) || published.PublishedAt.After(after) {
		t.Fatalf("发布时间应取注入时钟，实际 %v", published.PublishedAt)
	}

	n, err := e.st.CountEnrollments(ctx, a.ID)
	if err != nil {
		t.Fatalf("统计状态行失败: %v", err)
	}
	if n != 5 {
		t.Fatalf("发布应为 P01 的 5 名学生建立状态行，实际 %d", n)
	}
}

func TestPublishTwiceConflicts(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	teacher := e.user(t, "T01")

	a, err := e.svc.Create(ctx, teacher, validInput(e))
	if err != nil {
		t.Fatalf("创建草稿失败: %v", err)
	}
	if _, err := e.svc.Publish(ctx, teacher, a.ID); err != nil {
		t.Fatalf("首次发布失败: %v", err)
	}
	if _, err := e.svc.Publish(ctx, teacher, a.ID); err == nil || code(t, err) != "already_published" {
		t.Fatalf("重复发布应返回 already_published，实际 %v", err)
	}
}

func TestCrossClassIsNotFound(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	teacher := e.user(t, "T01")

	a, err := e.svc.Create(ctx, teacher, validInput(e))
	if err != nil {
		t.Fatalf("创建草稿失败: %v", err)
	}

	// 外班学生：角色不允许，先命中 forbidden_role。
	if _, err := e.svc.UpdateDraft(ctx, e.user(t, "X01"), a.ID, validInput(e)); err == nil || code(t, err) != "forbidden_role" {
		t.Fatalf("外班学生修改应被拒绝，实际 %v", err)
	}

	// 模拟"外班教师"：直接插入 P02 的教师账号，验证班级隔离返回 404 而非 403。
	if _, err := e.st.DB().Exec(`INSERT INTO users (id, class_id, name, role, password_hash, created_at)
		VALUES ('T02','P02','外班教师','teacher','x','2026-11-13T00:00:00Z')`); err != nil {
		t.Fatalf("插入外班教师失败: %v", err)
	}
	other := e.user(t, "T02")
	if _, err := e.svc.UpdateDraft(ctx, other, a.ID, validInput(e)); err == nil || code(t, err) != "not_found" {
		t.Fatalf("跨班修改应返回 not_found，实际 %v", err)
	}
	if _, err := e.svc.Publish(ctx, other, a.ID); err == nil || code(t, err) != "not_found" {
		t.Fatalf("跨班发布应返回 not_found，实际 %v", err)
	}
	if _, err := e.svc.UpdateDraft(ctx, other, "NOT-EXIST", validInput(e)); err == nil || code(t, err) != "not_found" {
		t.Fatalf("不存在作业应返回 not_found，实际 %v", err)
	}
}
