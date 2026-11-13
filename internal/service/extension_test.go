package service_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"onlineclass/internal/service"
	"onlineclass/internal/store"
)

func extInput(studentID string, deadline time.Time, reason string) service.ExtensionInput {
	return service.ExtensionInput{StudentID: studentID, DeadlineAt: deadline, Reason: reason}
}

// TestGrantExtensionOnlyAffectsThatStudent 补交只对授权学生生效，且每人只能一次。
func TestGrantExtensionOnlyAffectsThatStudent(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	e.seedPublished(t, "HW-PAST", e.clk.Now().Add(-time.Second))
	exts := service.NewExtensionService(e.st, e.clk)
	subs := service.NewSubmissionService(e.st, e.clk)
	teacher := e.user(t, "T01")

	deadline := e.clk.Now().Add(24 * time.Hour)
	ext, err := exts.Grant(ctx, teacher, "HW-PAST", extInput("S01", deadline, "生病住院，需要补交"))
	if err != nil {
		t.Fatalf("批准补交失败: %v", err)
	}
	if ext.StudentID != "S01" || ext.GrantedBy != "T01" || ext.DeadlineAt.Unix() != deadline.Unix() {
		t.Fatalf("补交记录不符: %+v", ext)
	}

	// 被授权学生可提交，并标记 extension_applied
	res, err := subs.Submit(ctx, e.user(t, "S01"), "HW-PAST", submitInput("补交作业", "req-s1"))
	if err != nil {
		t.Fatalf("补交后应可提交，实际 %v", err)
	}
	if !res.ExtensionApplied {
		t.Fatal("应标记 extension_applied=true")
	}

	// 其他学生不受影响
	_, err = subs.Submit(ctx, e.user(t, "S02"), "HW-PAST", submitInput("迟交", "req-s2"))
	if err == nil || code(t, err) != "deadline_passed" {
		t.Fatalf("补交只对授权学生生效，实际 %v", err)
	}

	// 二次设置 → 409 extension_exists（原因不同也不行）
	_, err = exts.Grant(ctx, teacher, "HW-PAST", extInput("S01", deadline.Add(time.Hour), "再次申请更晚"))
	if err == nil || code(t, err) != "extension_exists" {
		t.Fatalf("重复设置应返回 extension_exists，实际 %v", err)
	}

	list, err := e.st.ListExtensions(ctx, "HW-PAST")
	if err != nil {
		t.Fatalf("查询补交列表失败: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("应只有 1 条补交，实际 %d", len(list))
	}

	var events int
	if err := e.st.DB().QueryRow(
		`SELECT COUNT(1) FROM events WHERE assignment_id = 'HW-PAST' AND type = 'extension_granted'`).Scan(&events); err != nil {
		t.Fatalf("统计事件失败: %v", err)
	}
	if events != 1 {
		t.Fatalf("应写 1 条补交事件，实际 %d", events)
	}
}

// TestGrantExtensionValidation 原因长度、截止先后、角色与名单校验。
func TestGrantExtensionValidation(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	// 固定同一个基准时刻：否则两次 clk.Now() 的微秒差会让"等于原截止"被误判为更晚。
	base := e.clk.Now().Add(-time.Hour)
	e.seedPublished(t, "HW-PAST", base)
	exts := service.NewExtensionService(e.st, e.clk)
	teacher := e.user(t, "T01")
	future := e.clk.Now().Add(24 * time.Hour)

	cases := []struct {
		name   string
		actor  store.User
		id     string
		in     service.ExtensionInput
		expect string
	}{
		{"缺少学生", teacher, "HW-PAST", extInput("", future, "生病住院需要补交"), "invalid_input"},
		{"原因过短", teacher, "HW-PAST", extInput("S01", future, "生病"), "invalid_input"},
		{"原因为空", teacher, "HW-PAST", extInput("S01", future, "   "), "invalid_input"},
		{"原因过长", teacher, "HW-PAST", extInput("S01", future, strings.Repeat("字", 201)), "invalid_input"},
		{"缺少截止", teacher, "HW-PAST", extInput("S01", time.Time{}, "生病住院需要补交"), "invalid_input"},
		{"截止不晚于原截止", teacher, "HW-PAST", extInput("S01", base, "生病住院需要补交"), "extension_deadline_invalid"},
		{"截止早于原截止", teacher, "HW-PAST", extInput("S01", base.Add(-time.Hour), "生病住院需要补交"), "extension_deadline_invalid"},
		{"非学生账号", teacher, "HW-PAST", extInput("T01", future, "生病住院需要补交"), "not_found"},
		{"外班学生", teacher, "HW-PAST", extInput("X01", future, "生病住院需要补交"), "not_found"},
		{"不存在的学生", teacher, "HW-PAST", extInput("S99", future, "生病住院需要补交"), "not_found"},
		{"助教越权", e.user(t, "A01"), "HW-PAST", extInput("S01", future, "生病住院需要补交"), "forbidden_role"},
		{"学生越权", e.user(t, "S01"), "HW-PAST", extInput("S01", future, "生病住院需要补交"), "forbidden_role"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := exts.Grant(ctx, c.actor, c.id, c.in)
			if err == nil || code(t, err) != c.expect {
				t.Fatalf("%s 期望 %s，实际 %v", c.name, c.expect, err)
			}
		})
	}

	// 已通过的学生不需要补交
	if _, err := e.st.DB().Exec(
		`UPDATE enrollments SET state = 'PASSED' WHERE assignment_id = 'HW-PAST' AND student_id = 'S01'`); err != nil {
		t.Fatalf("构造已通过状态失败: %v", err)
	}
	if _, err := exts.Grant(ctx, teacher, "HW-PAST", extInput("S01", future, "生病住院需要补交")); err == nil || code(t, err) != "already_passed" {
		t.Fatalf("已通过学生应返回 already_passed，实际 %v", err)
	}

	// 同班但不在名单中（发布后新增的学生）
	if _, err := e.st.DB().Exec(
		`INSERT INTO users (id, class_id, name, role, password_hash, created_at)
		 VALUES ('S06', 'P01', '插班生', 'student', 'x', ?)`, e.clk.Now().Format(time.RFC3339)); err != nil {
		t.Fatalf("插入插班生失败: %v", err)
	}
	if _, err := exts.Grant(ctx, teacher, "HW-PAST", extInput("S06", future, "生病住院需要补交")); err == nil || code(t, err) != "not_enrolled" {
		t.Fatalf("未入名单学生应返回 not_enrolled，实际 %v", err)
	}

	// 草稿作业没有名单，也没有可补交的提交
	draft, err := e.svc.Create(ctx, teacher, validInput(e))
	if err != nil {
		t.Fatalf("创建草稿失败: %v", err)
	}
	if _, err := exts.Grant(ctx, teacher, draft.ID, extInput("S01", future, "生病住院需要补交")); err == nil || code(t, err) != "not_published" {
		t.Fatalf("草稿补交应返回 not_published，实际 %v", err)
	}
}
