package service_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"onlineclass/internal/service"
	"onlineclass/internal/store"
)

// createPublished 用服务创建并发布一份作业，发布时为 P01 五名学生建状态行。
func (e *svcEnv) createPublished(t *testing.T) store.Assignment {
	t.Helper()
	ctx := context.Background()
	teacher := e.user(t, "T01")
	a, err := e.svc.Create(ctx, teacher, validInput(e))
	if err != nil {
		t.Fatalf("创建作业失败: %v", err)
	}
	published, err := e.svc.Publish(ctx, teacher, a.ID)
	if err != nil {
		t.Fatalf("发布作业失败: %v", err)
	}
	return published
}

// seedPublished 直接写入一份已发布作业，用于构造"截止已过"等无法通过服务创建的场景。
func (e *svcEnv) seedPublished(t *testing.T, id string, deadline time.Time) {
	t.Helper()
	ts := deadline.UTC().Format(time.RFC3339Nano)
	created := e.clk.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339Nano)
	if _, err := e.st.DB().Exec(
		`INSERT INTO assignments
		 (id, class_id, teacher_id, title, requirement, deadline_at, state,
		  published_at, snapshot_title, snapshot_requirement, snapshot_deadline_at, snapshot_by, snapshot_at, created_at)
		 VALUES (?, 'P01', 'T01', '标题', '要求', ?, 'PUBLISHED', ?, '标题', '要求', ?, 'T01', ?, ?)`,
		id, ts, created, ts, created, created); err != nil {
		t.Fatalf("写入已发布作业失败: %v", err)
	}
	if _, err := e.st.DB().Exec(
		`INSERT INTO enrollments (assignment_id, student_id, state, state_version, current_version_no)
		 SELECT ?, id, 'NOT_SUBMITTED', 0, 0 FROM users WHERE class_id = 'P01' AND role = 'student'`,
		id); err != nil {
		t.Fatalf("写入状态行失败: %v", err)
	}
}

func submitInput(content, requestID string) service.SubmitInput {
	return service.SubmitInput{Content: content, RequestID: requestID}
}

func TestSubmitCreatesFirstVersion(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	a := e.createPublished(t)
	svc := service.NewSubmissionService(e.st, e.clk)

	res, err := svc.Submit(ctx, e.user(t, "S01"), a.ID, submitInput("第一版答案", "req-1"))
	if err != nil {
		t.Fatalf("首次提交失败: %v", err)
	}
	if res.Version.VersionNo != 1 || res.Enrollment.State != store.StateSubmitted {
		t.Fatalf("首次提交应生成 v1/SUBMITTED，实际 v%d/%s", res.Version.VersionNo, res.Enrollment.State)
	}
	if len(res.Version.ContentHash) != 64 {
		t.Fatalf("内容哈希应为 64 位十六进制，实际 %q", res.Version.ContentHash)
	}
	if res.ExtensionApplied {
		t.Fatal("未授权补交时不应标记 extension_applied")
	}
	if !res.EffectiveDeadline.Equal(a.SnapshotDeadlineAt) {
		t.Fatalf("有效截止应取作业快照: %v vs %v", res.EffectiveDeadline, a.SnapshotDeadlineAt)
	}

	enr, err := e.st.GetEnrollment(ctx, a.ID, "S01")
	if err != nil {
		t.Fatalf("读取状态行失败: %v", err)
	}
	if enr.State != store.StateSubmitted || enr.CurrentVersionNo != 1 || enr.StateVersion != 1 {
		t.Fatalf("状态行未推进: %+v", enr)
	}

	var events int
	if err := e.st.DB().QueryRow(
		`SELECT COUNT(1) FROM events WHERE assignment_id = ? AND student_id = ? AND type = 'submitted'`,
		a.ID, "S01").Scan(&events); err != nil {
		t.Fatalf("统计事件失败: %v", err)
	}
	if events != 1 {
		t.Fatalf("应写入 1 条提交事件，实际 %d", events)
	}
}

func TestResubmitCreatesSecondVersionAndKeepsHistory(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	a := e.createPublished(t)
	svc := service.NewSubmissionService(e.st, e.clk)
	s01 := e.user(t, "S01")

	if _, err := svc.Submit(ctx, s01, a.ID, submitInput("第一版", "req-1")); err != nil {
		t.Fatalf("首次提交失败: %v", err)
	}
	res, err := svc.Submit(ctx, s01, a.ID, submitInput("第二版", "req-2"))
	if err != nil {
		t.Fatalf("未批阅时更新应成功，实际 %v", err)
	}
	if res.Version.VersionNo != 2 {
		t.Fatalf("第二次提交应生成 v2，实际 v%d", res.Version.VersionNo)
	}

	versions, err := e.st.ListSubmissions(ctx, a.ID, "S01")
	if err != nil {
		t.Fatalf("查询版本失败: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("应保留两个版本，实际 %d", len(versions))
	}
	if versions[0].Content != "第一版" || versions[1].Content != "第二版" {
		t.Fatalf("历史版本内容被改动: %q / %q", versions[0].Content, versions[1].Content)
	}

	enr, err := e.st.GetEnrollment(ctx, a.ID, "S01")
	if err != nil {
		t.Fatalf("读取状态行失败: %v", err)
	}
	if enr.CurrentVersionNo != 2 || enr.StateVersion != 2 {
		t.Fatalf("状态行应推进到 v2，实际 %+v", enr)
	}
}

func TestSubmitAfterReturnedAllowed(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	a := e.createPublished(t)
	svc := service.NewSubmissionService(e.st, e.clk)
	s01 := e.user(t, "S01")

	if _, err := svc.Submit(ctx, s01, a.ID, submitInput("第一版", "req-1")); err != nil {
		t.Fatalf("首次提交失败: %v", err)
	}
	// D-07 之前没有批阅接口，这里直接构造"已退回"状态验证重交分支。
	if _, err := e.st.DB().Exec(
		`UPDATE enrollments SET state = 'RETURNED', state_version = state_version + 1
		 WHERE assignment_id = ? AND student_id = ?`, a.ID, "S01"); err != nil {
		t.Fatalf("构造退回状态失败: %v", err)
	}

	res, err := svc.Submit(ctx, s01, a.ID, submitInput("修改后重交", "req-2"))
	if err != nil {
		t.Fatalf("退回后应允许重新提交，实际 %v", err)
	}
	if res.Version.VersionNo != 2 {
		t.Fatalf("重交应生成 v2，实际 v%d", res.Version.VersionNo)
	}
}

func TestSubmitRejectedWhenPassed(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	a := e.createPublished(t)
	svc := service.NewSubmissionService(e.st, e.clk)

	if _, err := e.st.DB().Exec(
		`UPDATE enrollments SET state = 'PASSED', state_version = state_version + 1
		 WHERE assignment_id = ? AND student_id = 'S01'`, a.ID); err != nil {
		t.Fatalf("构造已通过状态失败: %v", err)
	}

	_, err := svc.Submit(ctx, e.user(t, "S01"), a.ID, submitInput("再交一次", "req-9"))
	if err == nil || code(t, err) != "already_passed" {
		t.Fatalf("已通过后提交应返回 already_passed，实际 %v", err)
	}
}

func TestSubmitDeadlineAndExtension(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	svc := service.NewSubmissionService(e.st, e.clk)
	s01 := e.user(t, "S01")

	// 截止已过 1 秒 → 拒绝
	e.seedPublished(t, "HW-PAST", e.clk.Now().Add(-time.Second))
	_, err := svc.Submit(ctx, s01, "HW-PAST", submitInput("迟交", "req-late"))
	if err == nil || code(t, err) != "deadline_passed" {
		t.Fatalf("逾期提交应返回 deadline_passed，实际 %v", err)
	}

	// 授权补交后同一作业可提交，且标记 extension_applied
	extDeadline := e.clk.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339Nano)
	if _, err := e.st.DB().Exec(
		`INSERT INTO extensions (assignment_id, student_id, deadline_at, reason, granted_by, granted_at)
		 VALUES ('HW-PAST', 'S01', ?, '生病住院', 'T01', ?)`,
		extDeadline, e.clk.Now().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("写入补交授权失败: %v", err)
	}
	res, err := svc.Submit(ctx, s01, "HW-PAST", submitInput("补交作业", "req-ext"))
	if err != nil {
		t.Fatalf("补交授权后应可提交，实际 %v", err)
	}
	if !res.ExtensionApplied {
		t.Fatal("应标记 extension_applied=true")
	}

	// 边界：截止在 1 秒后 → 允许；其他学生不受该生补交影响
	e.seedPublished(t, "HW-FUTURE", e.clk.Now().Add(time.Second))
	if _, err := svc.Submit(ctx, s01, "HW-FUTURE", submitInput("压线提交", "req-edge")); err != nil {
		t.Fatalf("截止前 1 秒应允许提交，实际 %v", err)
	}
	_, err = svc.Submit(ctx, e.user(t, "S02"), "HW-PAST", submitInput("他人逾期", "req-s02"))
	if err == nil || code(t, err) != "deadline_passed" {
		t.Fatalf("补交只对授权学生生效，S02 仍应逾期，实际 %v", err)
	}
}

func TestSubmitValidation(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	a := e.createPublished(t)
	svc := service.NewSubmissionService(e.st, e.clk)
	s01 := e.user(t, "S01")

	cases := []struct {
		name string
		in   service.SubmitInput
	}{
		{"空内容", submitInput("   ", "req-1")},
		{"内容超长", submitInput(strings.Repeat("字", 5001), "req-1")},
		{"缺少请求号", submitInput("内容", "")},
		{"请求号超长", submitInput("内容", strings.Repeat("r", 65))},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := svc.Submit(ctx, s01, a.ID, c.in); err == nil || code(t, err) != "invalid_input" {
				t.Fatalf("%s 应返回 invalid_input，实际 %v", c.name, err)
			}
		})
	}
}

func TestSubmitDuplicateRequestID(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	a := e.createPublished(t)
	svc := service.NewSubmissionService(e.st, e.clk)
	s01 := e.user(t, "S01")

	if _, err := svc.Submit(ctx, s01, a.ID, submitInput("第一版", "req-1")); err != nil {
		t.Fatalf("首次提交失败: %v", err)
	}
	_, err := svc.Submit(ctx, s01, a.ID, submitInput("同一请求号再发", "req-1"))
	if err == nil || code(t, err) != "duplicate_request_id" {
		t.Fatalf("重复请求号应返回 duplicate_request_id，实际 %v", err)
	}
	versions, err := e.st.ListSubmissions(ctx, a.ID, "S01")
	if err != nil {
		t.Fatalf("查询版本失败: %v", err)
	}
	if len(versions) != 1 {
		t.Fatalf("重复请求不得产生新版本，实际 %d", len(versions))
	}
}

func TestSubmitPermissionAndResourceChecks(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	a := e.createPublished(t)
	svc := service.NewSubmissionService(e.st, e.clk)

	for _, id := range []string{"T01", "A01"} {
		if _, err := svc.Submit(ctx, e.user(t, id), a.ID, submitInput("内容", "req-x")); err == nil || code(t, err) != "forbidden_role" {
			t.Fatalf("%s 提交应被拒绝，实际 %v", id, err)
		}
	}

	// 草稿不可提交（对学生按不存在处理）
	teacher := e.user(t, "T01")
	draft, err := e.svc.Create(ctx, teacher, validInput(e))
	if err != nil {
		t.Fatalf("创建草稿失败: %v", err)
	}
	if _, err := svc.Submit(ctx, e.user(t, "S01"), draft.ID, submitInput("内容", "req-d")); err == nil || code(t, err) != "not_found" {
		t.Fatalf("草稿提交应返回 not_found，实际 %v", err)
	}

	// 外班学生
	if _, err := svc.Submit(ctx, e.user(t, "X01"), a.ID, submitInput("内容", "req-c")); err == nil || code(t, err) != "not_found" {
		t.Fatalf("外班提交应返回 not_found，实际 %v", err)
	}

	// 发布后新入班的学生没有状态行
	if _, err := e.st.DB().Exec(
		`INSERT INTO users (id, class_id, name, role, password_hash, created_at)
		 VALUES ('S99', 'P01', '插班生', 'student', 'x', '2026-11-13T00:00:00Z')`); err != nil {
		t.Fatalf("插入插班生失败: %v", err)
	}
	if _, err := svc.Submit(ctx, e.user(t, "S99"), a.ID, submitInput("内容", "req-n")); err == nil || code(t, err) != "not_enrolled" {
		t.Fatalf("未入名单应返回 not_enrolled，实际 %v", err)
	}
}

func TestHistoryPermissions(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	a := e.createPublished(t)
	svc := service.NewSubmissionService(e.st, e.clk)

	if _, err := svc.Submit(ctx, e.user(t, "S01"), a.ID, submitInput("我的答案", "req-1")); err != nil {
		t.Fatalf("提交失败: %v", err)
	}

	h, err := svc.History(ctx, e.user(t, "S01"), a.ID, "")
	if err != nil {
		t.Fatalf("学生查看自己失败: %v", err)
	}
	if h.StudentID != "S01" || len(h.Versions) != 1 || h.State != store.StateSubmitted {
		t.Fatalf("历史记录不符: %+v", h)
	}

	if _, err := svc.History(ctx, e.user(t, "S01"), a.ID, "S02"); err == nil || code(t, err) != "forbidden_role" {
		t.Fatalf("学生查看同学应被拒绝，实际 %v", err)
	}

	if _, err := svc.History(ctx, e.user(t, "T01"), a.ID, "S01"); err != nil {
		t.Fatalf("教师查看本班学生应允许，实际 %v", err)
	}
	if _, err := svc.History(ctx, e.user(t, "A01"), a.ID, "S02"); err != nil {
		t.Fatalf("助教查看本班学生应允许，实际 %v", err)
	}
	if _, err := svc.History(ctx, e.user(t, "T01"), a.ID, "X01"); err == nil || code(t, err) != "not_found" {
		t.Fatalf("跨班查看应返回 not_found，实际 %v", err)
	}
	if _, err := svc.History(ctx, e.user(t, "X01"), a.ID, ""); err == nil || code(t, err) != "not_found" {
		t.Fatalf("外班查看作业应返回 not_found，实际 %v", err)
	}
}
