package service_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"onlineclass/internal/service"
	"onlineclass/internal/store"
)

// boardEnv 准备一份"四类都有"的作业：S01 待批阅、S02 通过、S03 退回、S04/S05 未交。
type boardEnv struct {
	env      *svcEnv
	assignID string
	subs     *service.SubmissionService
	revs     *service.ReviewService
	board    *service.OverviewService
}

func newBoardEnv(t *testing.T) boardEnv {
	t.Helper()
	e := newSvcEnv(t)
	ctx := context.Background()
	a := e.createPublished(t)
	subs := service.NewSubmissionService(e.st, e.clk)
	revs := service.NewReviewService(e.st, e.clk)
	teacher := e.user(t, "T01")

	s1, err := subs.Submit(ctx, e.user(t, "S01"), a.ID, submitInput("S01 答案", "r-s1"))
	if err != nil {
		t.Fatalf("S01 提交失败: %v", err)
	}
	s2, err := subs.Submit(ctx, e.user(t, "S02"), a.ID, submitInput("S02 答案", "r-s2"))
	if err != nil {
		t.Fatalf("S02 提交失败: %v", err)
	}
	s3, err := subs.Submit(ctx, e.user(t, "S03"), a.ID, submitInput("S03 答案", "r-s3"))
	if err != nil {
		t.Fatalf("S03 提交失败: %v", err)
	}
	if _, err := revs.Review(ctx, teacher, a.ID, reviewInput(s2.Version.ID, store.ReviewPass, "通过")); err != nil {
		t.Fatalf("S02 通过失败: %v", err)
	}
	if _, err := revs.Review(ctx, teacher, a.ID, reviewInput(s3.Version.ID, store.ReviewReject, "补充测试")); err != nil {
		t.Fatalf("S03 退回失败: %v", err)
	}
	_ = s1
	return boardEnv{
		env: e, assignID: a.ID, subs: subs, revs: revs,
		board: service.NewOverviewService(e.st, e.clk),
	}
}

func findStudent(list []service.StudentStatus, id string) service.StudentStatus {
	for _, st := range list {
		if st.StudentID == id {
			return st
		}
	}
	return service.StudentStatus{}
}

// TestBoardFourLists 四类计数、完成率与最近提交时间。
func TestBoardFourLists(t *testing.T) {
	b := newBoardEnv(t)
	ctx := context.Background()

	board, err := b.board.Board(ctx, b.env.user(t, "A01"), b.assignID)
	if err != nil {
		t.Fatalf("看板查询失败: %v", err)
	}
	want := map[string]int{"not_submitted": 2, "submitted": 1, "returned": 1, "passed": 1, "total": 5}
	for k, v := range want {
		if board.Counts[k] != v {
			t.Fatalf("counts[%s] 应为 %d，实际 %d（全部 %v）", k, v, board.Counts[k], board.Counts)
		}
	}
	if board.CompletionRate != 0.2 {
		t.Fatalf("完成率应为 0.2，实际 %v", board.CompletionRate)
	}

	s1 := findStudent(board.Students, "S01")
	if s1.State != store.StateSubmitted || s1.LastSubmittedAt.IsZero() {
		t.Fatalf("S01 应为待批阅且有提交时间，实际 %+v", s1)
	}
	s4 := findStudent(board.Students, "S04")
	if s4.State != store.StateNotSubmitted || s4.Overdue {
		t.Fatalf("S04 未交且未逾期，实际 %+v", s4)
	}
}

// TestBoardOverdueAndExtension 逾期标记只作用于"未交"，补交后改按个人截止计算。
func TestBoardOverdueAndExtension(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	e.seedPublished(t, "HW-PAST", e.clk.Now().Add(-time.Hour))
	boardSvc := service.NewOverviewService(e.st, e.clk)
	exts := service.NewExtensionService(e.st, e.clk)
	assistant := e.user(t, "A01")

	board, err := boardSvc.Board(ctx, assistant, "HW-PAST")
	if err != nil {
		t.Fatalf("看板查询失败: %v", err)
	}
	if !findStudent(board.Students, "S01").Overdue {
		t.Fatal("截止已过且未交，应标记逾期")
	}

	if _, err := exts.Grant(ctx, e.user(t, "T01"), "HW-PAST",
		extInput("S05", e.clk.Now().Add(24*time.Hour), "生病住院，需要补交")); err != nil {
		t.Fatalf("批准补交失败: %v", err)
	}
	board, err = boardSvc.Board(ctx, assistant, "HW-PAST")
	if err != nil {
		t.Fatalf("看板查询失败: %v", err)
	}
	s5 := findStudent(board.Students, "S05")
	if !s5.ExtensionApplied || s5.Overdue {
		t.Fatalf("补交学生不应再算逾期，实际 %+v", s5)
	}
	if !findStudent(board.Students, "S01").Overdue {
		t.Fatal("其他学生仍应逾期")
	}
}

// TestRecordReminderAppendOnly 助教可多次追加，教师/学生无权；记录保留历史。
func TestRecordReminderAppendOnly(t *testing.T) {
	b := newBoardEnv(t)
	ctx := context.Background()
	assistant := b.env.user(t, "A01")

	for i, note := range []string{"第一次电话未接", "第二次群里提醒"} {
		in := service.ReminderInput{StudentID: "S04", Channel: store.ChannelPhone, Note: note}
		if i == 1 {
			in.Channel = store.ChannelGroup
		}
		if _, err := b.board.RecordReminder(ctx, assistant, b.assignID, in); err != nil {
			t.Fatalf("第 %d 次催交失败: %v", i+1, err)
		}
	}

	board, err := b.board.Board(ctx, assistant, b.assignID)
	if err != nil {
		t.Fatalf("看板查询失败: %v", err)
	}
	if len(board.Reminders) != 2 {
		t.Fatalf("应保留 2 条催交记录，实际 %d", len(board.Reminders))
	}
	s4 := findStudent(board.Students, "S04")
	if s4.ReminderCount != 2 || s4.LastReminderAt.IsZero() {
		t.Fatalf("S04 催交计数不符: %+v", s4)
	}

	// 教师、学生都无权催交
	for _, actor := range []store.User{b.env.user(t, "T01"), b.env.user(t, "S01")} {
		if _, err := b.board.RecordReminder(ctx, actor, b.assignID,
			service.ReminderInput{StudentID: "S04", Channel: store.ChannelPhone, Note: "越权"}); err == nil || code(t, err) != "forbidden_role" {
			t.Fatalf("%s 催交应 403，实际 %v", actor.ID, err)
		}
	}
}

// TestRecordReminderValidation 方式白名单、摘要长度、名单与发布状态。
func TestRecordReminderValidation(t *testing.T) {
	b := newBoardEnv(t)
	ctx := context.Background()
	assistant := b.env.user(t, "A01")

	cases := []struct {
		name   string
		id     string
		in     service.ReminderInput
		expect string
	}{
		{"方式非法", b.assignID, service.ReminderInput{StudentID: "S04", Channel: "wechat", Note: "群里提醒"}, "invalid_input"},
		{"摘要为空", b.assignID, service.ReminderInput{StudentID: "S04", Channel: store.ChannelPhone, Note: "  "}, "invalid_input"},
		{"摘要过长", b.assignID, service.ReminderInput{StudentID: "S04", Channel: store.ChannelPhone, Note: strings.Repeat("催", 201)}, "invalid_input"},
		{"缺少学生", b.assignID, service.ReminderInput{StudentID: "", Channel: store.ChannelPhone, Note: "提醒"}, "invalid_input"},
		{"非本班学生", b.assignID, service.ReminderInput{StudentID: "X01", Channel: store.ChannelPhone, Note: "提醒"}, "not_found"},
		{"不存在的学生", b.assignID, service.ReminderInput{StudentID: "S99", Channel: store.ChannelPhone, Note: "提醒"}, "not_found"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := b.board.RecordReminder(ctx, assistant, c.id, c.in); err == nil || code(t, err) != c.expect {
				t.Fatalf("%s 期望 %s，实际 %v", c.name, c.expect, err)
			}
		})
	}

	// 草稿作业：助教看不到，按不存在处理
	draft, err := b.env.svc.Create(ctx, b.env.user(t, "T01"), validInput(b.env))
	if err != nil {
		t.Fatalf("创建草稿失败: %v", err)
	}
	_, err = b.board.RecordReminder(ctx, assistant, draft.ID,
		service.ReminderInput{StudentID: "S01", Channel: store.ChannelPhone, Note: "提醒"})
	if err == nil || code(t, err) != "not_found" {
		t.Fatalf("草稿不应可催交，实际 %v", err)
	}
}

// TestSummaryTeacherVsAssistant 教师有明细/流水/时间线，助教只有统计。
func TestSummaryTeacherVsAssistant(t *testing.T) {
	b := newBoardEnv(t)
	ctx := context.Background()

	teacherSummary, err := b.board.Summary(ctx, b.env.user(t, "T01"), b.assignID)
	if err != nil {
		t.Fatalf("教师汇总失败: %v", err)
	}
	if teacherSummary.ReadOnlyStats {
		t.Fatal("教师不应是只读统计")
	}
	if len(teacherSummary.Students) != 5 {
		t.Fatalf("教师应看到 5 名学生，实际 %d", len(teacherSummary.Students))
	}
	if len(teacherSummary.Points) != 1 || teacherSummary.TotalPoints != 10 {
		t.Fatalf("奖励流水应为 1 条 10 分，实际 %+v / %d", teacherSummary.Points, teacherSummary.TotalPoints)
	}
	if len(teacherSummary.Timeline) < 5 {
		t.Fatalf("时间线应包含 3 次提交 + 2 次批阅，实际 %d 条", len(teacherSummary.Timeline))
	}
	var s2 *service.StudentSummary
	for i := range teacherSummary.Students {
		if teacherSummary.Students[i].StudentID == "S02" {
			s2 = &teacherSummary.Students[i]
		}
	}
	if s2 == nil || s2.Points != 10 || s2.LatestReview == nil || s2.LatestReview.Decision != store.ReviewPass {
		t.Fatalf("S02 汇总应体现通过结论与 10 分，实际 %+v", s2)
	}
	if s2.VersionCount != 1 || s2.FirstSubmittedAt.IsZero() {
		t.Fatalf("S02 版本数与首次提交时间不符: %+v", s2)
	}

	assistantSummary, err := b.board.Summary(ctx, b.env.user(t, "A01"), b.assignID)
	if err != nil {
		t.Fatalf("助教汇总失败: %v", err)
	}
	if !assistantSummary.ReadOnlyStats || assistantSummary.Students != nil ||
		assistantSummary.Points != nil || assistantSummary.Timeline != nil {
		t.Fatalf("助教只应拿到统计，实际 %+v", assistantSummary)
	}
	if assistantSummary.Counts["passed"] != 1 || assistantSummary.CompletionRate != 0.2 {
		t.Fatalf("助教统计计数不符: %+v", assistantSummary.Counts)
	}

	// 学生无权查看班级汇总
	if _, err := b.board.Summary(ctx, b.env.user(t, "S01"), b.assignID); err == nil || code(t, err) != "forbidden_role" {
		t.Fatalf("学生查看汇总应 403，实际 %v", err)
	}
}

// TestSummaryTimelineCoversFullLifecycle 时间线从首次提交到发奖可追。
func TestSummaryTimelineCoversFullLifecycle(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	a := e.createPublished(t)
	subs := service.NewSubmissionService(e.st, e.clk)
	revs := service.NewReviewService(e.st, e.clk)
	overview := service.NewOverviewService(e.st, e.clk)
	teacher := e.user(t, "T01")
	student := e.user(t, "S01")

	v1, err := subs.Submit(ctx, student, a.ID, submitInput("第一版", "r1"))
	if err != nil {
		t.Fatalf("首次提交失败: %v", err)
	}
	if _, err := revs.Review(ctx, teacher, a.ID, reviewInput(v1.Version.ID, store.ReviewReject, "补充测试")); err != nil {
		t.Fatalf("退回失败: %v", err)
	}
	v2, err := subs.Submit(ctx, student, a.ID, submitAt("第二版", "r2", 1))
	if err != nil {
		t.Fatalf("重交失败: %v", err)
	}
	if _, err := revs.Review(ctx, teacher, a.ID, reviewInput(v2.Version.ID, store.ReviewPass, "通过")); err != nil {
		t.Fatalf("通过失败: %v", err)
	}

	summary, err := overview.Summary(ctx, teacher, a.ID)
	if err != nil {
		t.Fatalf("汇总失败: %v", err)
	}
	var types []string
	for _, ev := range summary.Timeline {
		if ev.StudentID == "S01" {
			types = append(types, ev.Type+":"+ev.ToState)
		}
	}
	want := []string{"submitted:SUBMITTED", "reviewed:RETURNED", "submitted:SUBMITTED", "reviewed:PASSED"}
	if len(types) != len(want) {
		t.Fatalf("S01 时间线应 %d 条，实际 %d 条：%v", len(want), len(types), types)
	}
	for i := range want {
		if types[i] != want[i] {
			t.Fatalf("第 %d 条事件应为 %s，实际 %s（全部 %v）", i+1, want[i], types[i], types)
		}
	}
	if summary.TotalPoints != 10 {
		t.Fatalf("最终应发 10 分，实际 %d", summary.TotalPoints)
	}
}
