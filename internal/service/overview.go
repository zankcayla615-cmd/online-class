package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"onlineclass/internal/apperr"
	"onlineclass/internal/clock"
	"onlineclass/internal/store"
)

// 催交摘要长度：题面只要求"沟通结果摘要"，未给上限，按 200 字控制。
const maxReminderNoteRunes = 200

// reminderChannels 是允许的催交方式（稳定代码 + 中文标签）。
var reminderChannels = map[string]string{
	store.ChannelInApp: "站内",
	store.ChannelPhone: "电话",
	store.ChannelGroup: "群",
}

// OverviewService 负责助教看板、催交记录与班级汇总（只读统计）。
type OverviewService struct {
	store *store.Store
	clk   *clock.Clock
}

// NewOverviewService 构造看板/汇总服务。
func NewOverviewService(st *store.Store, clk *clock.Clock) *OverviewService {
	return &OverviewService{store: st, clk: clk}
}

// StudentStatus 是看板的一行：只有状态与时间，**不含答案正文**。
type StudentStatus struct {
	StudentID         string
	Name              string
	State             string
	CurrentVersionNo  int
	EffectiveDeadline time.Time
	ExtensionApplied  bool
	Overdue           bool
	LastSubmittedAt   time.Time
	ReminderCount     int
	LastReminderAt    time.Time
}

// ClassBoard 是四类清单 + 催交记录。
type ClassBoard struct {
	Assignment     store.Assignment
	GeneratedAt    time.Time
	Counts         map[string]int
	CompletionRate float64
	Students       []StudentStatus
	Reminders      []store.Reminder
}

// LatestReview 是某生最近一次批阅结论（汇总用）。
type LatestReview struct {
	Decision  string
	Feedback  string
	CreatedAt time.Time
}

// StudentSummary 是汇总的一名学生：状态、版本数、补交、积分与最近结论。
type StudentSummary struct {
	StudentStatus
	VersionCount     int
	FirstSubmittedAt time.Time
	Extension        *store.Extension
	Points           int
	LatestReview     *LatestReview
}

// ClassSummary 是班级汇总。ReadOnlyStats=true 表示调用者是助教：
// 只返回计数与完成率，不带人均明细、奖励流水与时间线。
type ClassSummary struct {
	Assignment     store.Assignment
	GeneratedAt    time.Time
	ReadOnlyStats  bool
	Counts         map[string]int
	CompletionRate float64
	Students       []StudentSummary
	Points         []store.PointsEntry
	TotalPoints    int
	Timeline       []store.Event
}

// Board 返回四类清单。教师与助教都可看，但都不含正文。
func (s *OverviewService) Board(ctx context.Context, actor store.User, assignmentID string) (ClassBoard, error) {
	a, err := s.loadPublished(ctx, actor, assignmentID, true)
	if err != nil {
		return ClassBoard{}, err
	}

	rows, err := s.store.ListEnrollmentRows(ctx, a.ID)
	if err != nil {
		return ClassBoard{}, err
	}
	exts, err := s.store.ListExtensions(ctx, a.ID)
	if err != nil {
		return ClassBoard{}, err
	}
	reminders, err := s.store.ListReminders(ctx, a.ID)
	if err != nil {
		return ClassBoard{}, err
	}

	now := s.clk.Now()
	extByStudent := make(map[string]store.Extension, len(exts))
	for _, ext := range exts {
		extByStudent[ext.StudentID] = ext
	}
	reminderCount := make(map[string]int, len(rows))
	lastReminder := make(map[string]time.Time, len(rows))
	for _, r := range reminders {
		reminderCount[r.StudentID]++
		if r.CreatedAt.After(lastReminder[r.StudentID]) {
			lastReminder[r.StudentID] = r.CreatedAt
		}
	}

	students := make([]StudentStatus, 0, len(rows))
	for _, row := range rows {
		st := StudentStatus{
			StudentID:        row.StudentID,
			Name:             row.StudentName,
			State:            row.State,
			CurrentVersionNo: row.CurrentVersionNo,
			LastSubmittedAt:  row.LastSubmittedAt,
			ReminderCount:    reminderCount[row.StudentID],
			LastReminderAt:   lastReminder[row.StudentID],
		}
		st.EffectiveDeadline = a.EffectiveDeadline()
		if ext, ok := extByStudent[row.StudentID]; ok {
			st.EffectiveDeadline = ext.DeadlineAt
			st.ExtensionApplied = true
		}
		st.Overdue = isOverdue(st.State, st.EffectiveDeadline, now)
		students = append(students, st)
	}

	return ClassBoard{
		Assignment:     a,
		GeneratedAt:    now,
		Counts:         countsOf(students),
		CompletionRate: completionRate(students),
		Students:       students,
		Reminders:      reminders,
	}, nil
}

// Summary 返回班级汇总。教师拿到明细/流水/时间线；助教只拿到统计。
func (s *OverviewService) Summary(ctx context.Context, actor store.User, assignmentID string) (ClassSummary, error) {
	a, err := s.loadPublished(ctx, actor, assignmentID, true)
	if err != nil {
		return ClassSummary{}, err
	}

	rows, err := s.store.ListEnrollmentRows(ctx, a.ID)
	if err != nil {
		return ClassSummary{}, err
	}
	exts, err := s.store.ListExtensions(ctx, a.ID)
	if err != nil {
		return ClassSummary{}, err
	}
	now := s.clk.Now()
	extByStudent := make(map[string]store.Extension, len(exts))
	for _, ext := range exts {
		extByStudent[ext.StudentID] = ext
	}

	students := make([]StudentSummary, 0, len(rows))
	for _, row := range rows {
		item := StudentSummary{
			StudentStatus: StudentStatus{
				StudentID:        row.StudentID,
				Name:             row.StudentName,
				State:            row.State,
				CurrentVersionNo: row.CurrentVersionNo,
				LastSubmittedAt:  row.LastSubmittedAt,
			},
			VersionCount:     row.SubmissionCount,
			FirstSubmittedAt: row.FirstSubmittedAt,
		}
		item.EffectiveDeadline = a.EffectiveDeadline()
		if ext, ok := extByStudent[row.StudentID]; ok {
			extCopy := ext
			item.Extension = &extCopy
			item.EffectiveDeadline = ext.DeadlineAt
			item.ExtensionApplied = true
		}
		item.Overdue = isOverdue(item.State, item.EffectiveDeadline, now)

		// 最近一次批阅结论：只取一条用于"最终结果"，完整历史由时间线承载。
		reviews, err := s.store.ListReviews(ctx, a.ID, row.StudentID)
		if err != nil {
			return ClassSummary{}, err
		}
		if len(reviews) > 0 {
			last := reviews[len(reviews)-1]
			item.LatestReview = &LatestReview{
				Decision:  last.Decision,
				Feedback:  last.Feedback,
				CreatedAt: last.CreatedAt,
			}
		}
		students = append(students, item)
	}

	entries, err := s.store.ListPointsByAssignment(ctx, a.ID)
	if err != nil {
		return ClassSummary{}, err
	}
	pointsByStudent := make(map[string]int, len(students))
	total := 0
	for _, e := range entries {
		pointsByStudent[e.StudentID] += e.Points
		total += e.Points
	}
	for i := range students {
		students[i].Points = pointsByStudent[students[i].StudentID]
	}

	events, err := s.store.ListEvents(ctx, a.ID, "")
	if err != nil {
		return ClassSummary{}, err
	}

	summary := ClassSummary{
		Assignment:     a,
		GeneratedAt:    now,
		Counts:         countsOfStatuses(students),
		CompletionRate: completionRateStatuses(students),
	}
	if actor.Role == store.RoleAssistant {
		// 助教：只读统计，不返回人均明细、奖励流水与时间线（AS-07 最小暴露）。
		summary.ReadOnlyStats = true
		return summary, nil
	}
	summary.Students = students
	summary.Points = entries
	summary.TotalPoints = total
	summary.Timeline = events
	return summary, nil
}

// ReminderInput 是催交记录的请求体。
type ReminderInput struct {
	StudentID string
	Channel   string
	Note      string
}

// RecordReminder 追加一条催交记录（只追加，不修改历史）。
func (s *OverviewService) RecordReminder(ctx context.Context, actor store.User, assignmentID string, in ReminderInput) (store.Reminder, error) {
	if actor.Role != store.RoleAssistant {
		return store.Reminder{}, apperr.Forbidden("只有助教可以记录催交")
	}
	a, err := loadAssignmentFor(ctx, s.store, actor, assignmentID)
	if err != nil {
		return store.Reminder{}, err
	}
	if !a.Published() {
		// 草稿对助教不可见：按不存在处理，避免泄露"有份草稿"。
		return store.Reminder{}, apperr.NotFound("作业不存在")
	}

	studentID, channel, note, err := validateReminder(in)
	if err != nil {
		return store.Reminder{}, err
	}

	target, err := s.store.GetUser(ctx, studentID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.Reminder{}, apperr.NotFound("学生不存在")
		}
		return store.Reminder{}, err
	}
	if target.ClassID != actor.ClassID || target.Role != store.RoleStudent {
		return store.Reminder{}, apperr.NotFound("学生不存在")
	}
	if _, err := s.store.GetEnrollment(ctx, assignmentID, studentID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.Reminder{}, apperr.Conflict("not_enrolled", "该学生不在作业名单中").
				WithDetails(map[string]any{"student_id": studentID})
		}
		return store.Reminder{}, err
	}

	r := store.Reminder{
		ID:           newReminderID(),
		AssignmentID: assignmentID,
		StudentID:    studentID,
		AssistantID:  actor.ID,
		Channel:      channel,
		Note:         note,
		CreatedAt:    s.clk.Now(),
	}
	if err := s.store.CreateReminder(ctx, r); err != nil {
		return store.Reminder{}, err
	}
	return r, nil
}

// loadPublished 统一处理"谁能看 / 作业是否已发布"。
func (s *OverviewService) loadPublished(ctx context.Context, actor store.User, assignmentID string, assistantAllowed bool) (store.Assignment, error) {
	switch {
	case actor.Role == store.RoleTeacher:
	case actor.Role == store.RoleAssistant && assistantAllowed:
	default:
		return store.Assignment{}, apperr.Forbidden("当前角色无权查看班级统计")
	}
	a, err := loadAssignmentFor(ctx, s.store, actor, assignmentID)
	if err != nil {
		return store.Assignment{}, err
	}
	if !a.Published() {
		// 草稿只有教师本人可见：教师得到"未发布"，其他角色按不存在处理，避免泄露草稿。
		if actor.Role == store.RoleTeacher {
			return store.Assignment{}, apperr.Conflict("not_published", "作业尚未发布，暂无班级数据")
		}
		return store.Assignment{}, apperr.NotFound("作业不存在")
	}
	return a, nil
}

func isOverdue(state string, deadline, now time.Time) bool {
	// 只有"从未提交且已过有效截止"才算逾期未交；退回/待批阅有自己的类别。
	return state == store.StateNotSubmitted && now.After(deadline)
}

func countsOf(students []StudentStatus) map[string]int {
	counts := map[string]int{
		"not_submitted": 0, "submitted": 0, "returned": 0, "passed": 0, "total": len(students),
	}
	for _, st := range students {
		switch st.State {
		case store.StateNotSubmitted:
			counts["not_submitted"]++
		case store.StateSubmitted:
			counts["submitted"]++
		case store.StateReturned:
			counts["returned"]++
		case store.StatePassed:
			counts["passed"]++
		}
	}
	return counts
}

func completionRate(students []StudentStatus) float64 {
	if len(students) == 0 {
		return 0
	}
	passed := 0
	for _, st := range students {
		if st.State == store.StatePassed {
			passed++
		}
	}
	return round3(float64(passed) / float64(len(students)))
}

func countsOfStatuses(students []StudentSummary) map[string]int {
	asStatus := make([]StudentStatus, 0, len(students))
	for _, st := range students {
		asStatus = append(asStatus, st.StudentStatus)
	}
	return countsOf(asStatus)
}

func completionRateStatuses(students []StudentSummary) float64 {
	asStatus := make([]StudentStatus, 0, len(students))
	for _, st := range students {
		asStatus = append(asStatus, st.StudentStatus)
	}
	return completionRate(asStatus)
}

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }

func validateReminder(in ReminderInput) (string, string, string, error) {
	studentID := strings.TrimSpace(in.StudentID)
	channel := strings.TrimSpace(in.Channel)
	note := strings.TrimSpace(in.Note)
	if _, ok := reminderChannels[channel]; !ok {
		return "", "", "", apperr.Invalid("channel 只能是 in_app、phone 或 group")
	}
	switch {
	case studentID == "":
		return "", "", "", apperr.Invalid("student_id 不能为空")
	case note == "":
		return "", "", "", apperr.Invalid("沟通结果摘要不能为空")
	case utf8.RuneCountInString(note) > maxReminderNoteRunes:
		return "", "", "", apperr.Invalid("沟通结果摘要不能超过 200 个字")
	}
	return studentID, channel, note, nil
}

func newReminderID() string {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		// 随机源失败时退化为时间戳，保证仍可写入（概率极低）。
		return fmt.Sprintf("RM-%d", time.Now().UnixNano())
	}
	return "RM-" + hex.EncodeToString(buf)
}
