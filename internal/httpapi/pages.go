package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"onlineclass/internal/apperr"
	"onlineclass/internal/service"
	"onlineclass/internal/store"
)

// 页面只做三件事：取会话身份、调服务、渲染结果；规则判定一律留在 service。
//
// 所有表单提交都走 PRG（Post-Redirect-Get）：成功跳回页面并带 `ok=` 白名单代码，
// 失败直接渲染错误页（错误码 + 中文说明），避免刷新导致重复提交。

var okMessages = map[string]string{
	"created":   "草稿已创建，可继续修改或发布",
	"updated":   "草稿已保存",
	"published": "作业已发布：内容已锁定，学生现在可以提交",
	"submitted": "提交成功",
	"reviewed":  "批阅结论已保存",
	"extended":  "补交已批准，仅对该学生生效",
	"reminded":  "催交已记录（历史不可修改）",
}

var (
	stateLabels = map[string]string{
		store.StateNotSubmitted: "未提交",
		store.StateSubmitted:    "待批阅",
		store.StateReturned:     "已退回",
		store.StatePassed:       "已通过",
	}
	assignmentStateLabels = map[string]string{
		store.AssignmentDraft:     "草稿",
		store.AssignmentPublished: "已发布",
	}
	decisionLabels = map[string]string{
		store.ReviewPass:   "通过",
		store.ReviewReject: "退回",
	}
	channelLabels = map[string]string{
		store.ChannelInApp: "站内",
		store.ChannelPhone: "电话",
		store.ChannelGroup: "群",
	}
	eventTypeLabels = map[string]string{
		"submitted":         "提交",
		"reviewed":          "批阅",
		"extension_granted": "补交批准",
	}
)

func labelOf(m map[string]string, key string) string {
	if v, ok := m[key]; ok {
		return v
	}
	return key
}

// pageBase 是所有页面共用的头部数据。
type pageBase struct {
	User   store.User
	Role   string
	Now    string
	Offset string
	OK     string
}

func (s *Server) baseData(u store.User) pageBase {
	return pageBase{
		User:   u,
		Role:   roleLabel(u.Role),
		Now:    s.clk.Now().Format("2006-01-02 15:04"),
		Offset: s.clk.Offset().String(),
	}
}

func (s *Server) renderPage(w http.ResponseWriter, status int, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := s.tpl.ExecuteTemplate(w, name, data); err != nil {
		logError("渲染页面 "+name, err)
	}
}

// okMessage 只翻译白名单代码，不把查询串原样回显到页面。
func okMessage(r *http.Request) string {
	msg, ok := okMessages[r.URL.Query().Get("ok")]
	if !ok {
		return ""
	}
	if v := r.URL.Query().Get("v"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			msg += "（第 " + strconv.Itoa(n) + " 版）"
		}
	}
	if p := r.URL.Query().Get("p"); p != "" {
		if n, err := strconv.Atoi(p); err == nil && n > 0 {
			msg += "，本次发放 " + strconv.Itoa(n) + " 分"
		}
	}
	return msg
}

// pageUser 取会话身份；未登录跳登录页。
func (s *Server) pageUser(w http.ResponseWriter, r *http.Request) (store.User, bool) {
	u, ok := userFrom(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return store.User{}, false
	}
	return u, true
}

// renderPageError 把业务错误渲染成可读页面：状态码 + 错误码 + 中文说明 + 返回入口。
func (s *Server) renderPageError(w http.ResponseWriter, r *http.Request, err error, back string) {
	status, code, msg := http.StatusInternalServerError, "internal", "服务内部错误，请稍后重试"
	var ae *apperr.Error
	if errors.As(err, &ae) {
		status, code, msg = ae.Status, ae.Code, ae.Message
	} else {
		logError("页面业务处理", err)
	}
	u, _ := userFrom(r)
	s.renderPage(w, status, "error.html", map[string]any{
		"Base":    s.baseData(u),
		"Code":    code,
		"Message": msg,
		"Back":    back,
	})
}

// handleRoot 按角色分流到各自首页。
func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.pageUser(w, r); !ok {
		return
	}
	http.Redirect(w, r, "/assignments", http.StatusSeeOther)
}

type assignmentCard struct {
	ID          string
	Title       string
	State       string
	StateLabel  string
	Deadline    string
	PublishedAt string
	IsDraft     bool
	CanReview   bool
}

type assignmentsPageData struct {
	Base        pageBase
	Assignments []assignmentCard
	IsTeacher   bool
	DefaultTime string
	// 编辑草稿时复用同一个表单：FormAction 指向更新接口，字段预填。
	EditID          string
	EditTitle       string
	EditRequirement string
	EditDeadline    string
	FormAction      string
	Editing         bool
}

// handleAssignmentsPage 是发布页：教师见新建表单与全部作业；学生/助教只见已发布。
func (s *Server) handleAssignmentsPage(w http.ResponseWriter, r *http.Request) {
	u, ok := s.pageUser(w, r)
	if !ok {
		return
	}
	list, err := s.store.ListAssignmentsByClass(r.Context(), u.ClassID)
	if err != nil {
		s.renderPageError(w, r, err, "/assignments")
		return
	}

	cards := make([]assignmentCard, 0, len(list))
	for _, a := range list {
		if u.Role != store.RoleTeacher && !a.Published() {
			continue // 草稿对学生/助教不可见
		}
		cards = append(cards, assignmentCard{
			ID:          a.ID,
			Title:       a.EffectiveTitle(),
			State:       a.State,
			StateLabel:  labelOf(assignmentStateLabels, a.State),
			Deadline:    a.EffectiveDeadline().Format("2006-01-02 15:04"),
			PublishedAt: formatPageTime(a.PublishedAt),
			IsDraft:     a.State == store.AssignmentDraft,
			CanReview:   a.Published(),
		})
	}

	data := assignmentsPageData{
		Base:         s.baseData(u),
		Assignments:  cards,
		IsTeacher:    u.Role == store.RoleTeacher,
		DefaultTime:  s.clk.Now().Add(24 * time.Hour).Format("2006-01-02T15:04"),
		FormAction:   "/assignments",
		EditDeadline: s.clk.Now().Add(24 * time.Hour).Format("2006-01-02T15:04"),
	}
	if edit := r.URL.Query().Get("edit"); edit != "" {
		if u.Role != store.RoleTeacher {
			s.renderPageError(w, r, apperr.Forbidden("只有教师可以编辑作业"), "/assignments")
			return
		}
		a, err := s.assignmentForPage(r, u, edit)
		if err != nil {
			s.renderPageError(w, r, err, "/assignments")
			return
		}
		if a.State != store.AssignmentDraft {
			s.renderPageError(w, r, apperr.Conflict("assignment_locked", "作业已发布，内容已锁定，不能修改"), "/assignments")
			return
		}
		data.EditID = a.ID
		data.EditTitle = a.Title
		data.EditRequirement = a.Requirement
		data.EditDeadline = a.DeadlineAt.In(time.Local).Format("2006-01-02T15:04")
		data.FormAction = "/assignments/" + a.ID
		data.Editing = true
	}
	data.Base.OK = okMessage(r)
	s.renderPage(w, http.StatusOK, "assignments.html", data)
}

// handleAssignmentCreatePage 处理新建草稿表单。
func (s *Server) handleAssignmentCreatePage(w http.ResponseWriter, r *http.Request) {
	u, ok := s.pageUser(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderPageError(w, r, apperr.Invalid("表单格式错误"), "/assignments")
		return
	}
	deadline, err := parseFormDeadline(r.PostFormValue("deadline_at"))
	if err != nil {
		s.renderPageError(w, r, err, "/assignments")
		return
	}
	if _, err := s.assignments.Create(r.Context(), u, service.AssignmentInput{
		Title:       r.PostFormValue("title"),
		Requirement: r.PostFormValue("requirement"),
		DeadlineAt:  deadline,
	}); err != nil {
		s.renderPageError(w, r, err, "/assignments")
		return
	}
	http.Redirect(w, r, "/assignments?ok=created", http.StatusSeeOther)
}

// handleAssignmentUpdatePage 处理草稿修改表单（已发布会 409 assignment_locked）。
func (s *Server) handleAssignmentUpdatePage(w http.ResponseWriter, r *http.Request) {
	u, ok := s.pageUser(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		s.renderPageError(w, r, apperr.Invalid("表单格式错误"), "/assignments")
		return
	}
	deadline, err := parseFormDeadline(r.PostFormValue("deadline_at"))
	if err != nil {
		s.renderPageError(w, r, err, "/assignments")
		return
	}
	if _, err := s.assignments.UpdateDraft(r.Context(), u, id, service.AssignmentInput{
		Title:       r.PostFormValue("title"),
		Requirement: r.PostFormValue("requirement"),
		DeadlineAt:  deadline,
	}); err != nil {
		s.renderPageError(w, r, err, "/assignments")
		return
	}
	http.Redirect(w, r, "/assignments?ok=updated", http.StatusSeeOther)
}

// handleAssignmentPublishPage 处理发布按钮。
func (s *Server) handleAssignmentPublishPage(w http.ResponseWriter, r *http.Request) {
	u, ok := s.pageUser(w, r)
	if !ok {
		return
	}
	if _, err := s.assignments.Publish(r.Context(), u, r.PathValue("id")); err != nil {
		s.renderPageError(w, r, err, "/assignments")
		return
	}
	http.Redirect(w, r, "/assignments?ok=published", http.StatusSeeOther)
}

type versionRow struct {
	No         int
	Content    string
	CreatedAt  string
	Decision   string
	Feedback   string
	ReviewedAt string
}

type studentPageData struct {
	Base             pageBase
	Assignment       store.Assignment
	Title            string
	Requirement      string
	Deadline         string
	State            string
	StateLabel       string
	CurrentVersionNo int
	Extension        *store.Extension
	Versions         []versionRow
	Points           int
	Entries          []store.PointsEntry
	RequestID        string
	CanSubmit        bool
	BlockedReason    string
	DraftContent     string
}

// handleStudentAssignmentPage 是学生作业页；教师/助教访问时各自跳转到本角色页面。
func (s *Server) handleStudentAssignmentPage(w http.ResponseWriter, r *http.Request) {
	u, ok := s.pageUser(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	switch u.Role {
	case store.RoleTeacher:
		http.Redirect(w, r, "/assignments/"+id+"/review", http.StatusSeeOther)
		return
	case store.RoleAssistant:
		http.Redirect(w, r, "/assignments/"+id+"/board", http.StatusSeeOther)
		return
	}

	a, err := s.assignmentForPage(r, u, id)
	if err != nil {
		s.renderPageError(w, r, err, "/assignments")
		return
	}
	if !a.Published() {
		s.renderPageError(w, r, apperr.NotFound("作业不存在"), "/assignments")
		return
	}

	h, err := s.submissions.History(r.Context(), u, id, "")
	if err != nil {
		s.renderPageError(w, r, err, "/assignments")
		return
	}
	versions := make([]versionRow, 0, len(h.Versions))
	for _, v := range h.Versions {
		row := versionRow{No: v.VersionNo, Content: v.Content, CreatedAt: formatPageTime(v.CreatedAt)}
		if rev, ok := h.Reviews[v.VersionNo]; ok {
			row.Decision = labelOf(decisionLabels, rev.Decision)
			row.Feedback = rev.Feedback
			row.ReviewedAt = formatPageTime(rev.CreatedAt)
		}
		versions = append(versions, row)
	}

	points, err := s.store.TotalPoints(r.Context(), u.ID)
	if err != nil {
		s.renderPageError(w, r, err, "/assignments")
		return
	}
	entries, err := s.store.ListPointsByAssignment(r.Context(), id)
	if err != nil {
		s.renderPageError(w, r, err, "/assignments")
		return
	}
	own := make([]store.PointsEntry, 0, 1)
	for _, e := range entries {
		if e.StudentID == u.ID {
			own = append(own, e)
		}
	}

	now := s.clk.Now()
	blocked := ""
	switch {
	case h.State == store.StatePassed:
		blocked = "已通过，无需再提交"
	case now.After(h.EffectiveDeadline):
		blocked = "已超过有效截止时间（" + h.EffectiveDeadline.Format("2006-01-02 15:04") + "）"
	}

	data := studentPageData{
		Base:             s.baseData(u),
		Assignment:       a,
		Title:            a.EffectiveTitle(),
		Requirement:      a.EffectiveRequirement(),
		Deadline:         h.EffectiveDeadline.Format("2006-01-02 15:04"),
		State:            h.State,
		StateLabel:       labelOf(stateLabels, h.State),
		CurrentVersionNo: h.CurrentVersionNo,
		Versions:         versions,
		Points:           points,
		Entries:          own,
		RequestID:        newPageRequestID(),
		CanSubmit:        blocked == "",
		BlockedReason:    blocked,
	}
	if n := len(h.Versions); n > 0 {
		data.DraftContent = h.Versions[n-1].Content
	}
	if h.ExtensionApplied {
		if ext, err := s.store.GetExtension(r.Context(), id, u.ID); err == nil {
			extCopy := ext
			data.Extension = &extCopy
		}
	}
	data.Base.OK = okMessage(r)
	s.renderPage(w, http.StatusOK, "assignment.html", data)
}

// handleSubmitPage 处理学生提交/更新表单。
func (s *Server) handleSubmitPage(w http.ResponseWriter, r *http.Request) {
	u, ok := s.pageUser(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		s.renderPageError(w, r, apperr.Invalid("表单格式错误"), "/assignments/"+id)
		return
	}
	baseVersion, err := strconv.Atoi(r.PostFormValue("base_version"))
	if err != nil {
		s.renderPageError(w, r, apperr.Invalid("base_version 必须是整数"), "/assignments/"+id)
		return
	}
	res, err := s.submissions.Submit(r.Context(), u, id, service.SubmitInput{
		Content:     r.PostFormValue("content"),
		RequestID:   r.PostFormValue("request_id"),
		BaseVersion: &baseVersion,
	})
	if err != nil {
		s.renderPageError(w, r, err, "/assignments/"+id)
		return
	}
	target := "/assignments/" + id + "?ok=submitted&v=" + strconv.Itoa(res.Version.VersionNo)
	http.Redirect(w, r, target, http.StatusSeeOther)
}

type reviewRow struct {
	StudentID   string
	Name        string
	State       string
	StateLabel  string
	VersionID   string
	VersionNo   int
	Content     string
	SubmittedAt string
	Decision    string
	DecisionAt  string
	Feedback    string
	Overdue     bool
	Extension   *store.Extension
	RequestID   string
	CanReview   bool
}

type reviewPageData struct {
	Base        pageBase
	Assignment  store.Assignment
	Title       string
	Deadline    string
	Rows        []reviewRow
	Extendable  []reviewRow
	Pending     int
	Done        int
	DefaultTime string
}

// handleReviewPage 是教师批阅台：按当前版本批阅，旧版本不可批。
func (s *Server) handleReviewPage(w http.ResponseWriter, r *http.Request) {
	u, ok := s.pageUser(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if u.Role != store.RoleTeacher {
		s.renderPageError(w, r, apperr.Forbidden("只有教师可以进入批阅台"), "/assignments")
		return
	}

	board, err := s.overview.Board(r.Context(), u, id)
	if err != nil {
		s.renderPageError(w, r, err, "/assignments")
		return
	}

	rows := make([]reviewRow, 0, len(board.Students))
	pending, done := 0, 0
	for _, st := range board.Students {
		row := reviewRow{
			StudentID:  st.StudentID,
			Name:       st.Name,
			State:      st.State,
			StateLabel: labelOf(stateLabels, st.State),
			Overdue:    st.Overdue,
			RequestID:  newPageRequestID(),
		}
		h, err := s.submissions.History(r.Context(), u, id, st.StudentID)
		if err != nil {
			s.renderPageError(w, r, err, "/assignments/"+id+"/review")
			return
		}
		if n := len(h.Versions); n > 0 {
			latest := h.Versions[n-1]
			row.VersionID = latest.ID
			row.VersionNo = latest.VersionNo
			row.Content = latest.Content
			row.SubmittedAt = formatPageTime(latest.CreatedAt)
			if rev, ok := h.Reviews[latest.VersionNo]; ok {
				row.Decision = labelOf(decisionLabels, rev.Decision)
				row.Feedback = rev.Feedback
				row.DecisionAt = formatPageTime(rev.CreatedAt)
			}
		}
		if h.ExtensionApplied {
			if ext, err := s.store.GetExtension(r.Context(), id, st.StudentID); err == nil {
				extCopy := ext
				row.Extension = &extCopy
			}
		}
		row.CanReview = st.State == store.StateSubmitted && row.VersionID != ""
		if row.CanReview {
			pending++
		}
		if st.State == store.StatePassed {
			done++
		}
		rows = append(rows, row)
	}

	data := reviewPageData{
		Base:        s.baseData(u),
		Assignment:  board.Assignment,
		Title:       board.Assignment.EffectiveTitle(),
		Deadline:    board.Assignment.EffectiveDeadline().Format("2006-01-02 15:04"),
		Rows:        rows,
		Pending:     pending,
		Done:        done,
		DefaultTime: s.clk.Now().Add(24 * time.Hour).Format("2006-01-02T15:04"),
	}
	for _, row := range rows {
		if row.State != store.StatePassed && row.Extension == nil {
			data.Extendable = append(data.Extendable, row)
		}
	}
	data.Base.OK = okMessage(r)
	s.renderPage(w, http.StatusOK, "review.html", data)
}

// handleReviewSubmitPage 处理批阅表单。
func (s *Server) handleReviewSubmitPage(w http.ResponseWriter, r *http.Request) {
	u, ok := s.pageUser(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		s.renderPageError(w, r, apperr.Invalid("表单格式错误"), "/assignments/"+id+"/review")
		return
	}
	res, err := s.reviews.Review(r.Context(), u, id, service.ReviewInput{
		VersionID: r.PostFormValue("version_id"),
		Decision:  r.PostFormValue("decision"),
		Feedback:  r.PostFormValue("feedback"),
		RequestID: r.PostFormValue("request_id"),
	})
	if err != nil {
		s.renderPageError(w, r, err, "/assignments/"+id+"/review")
		return
	}
	target := "/assignments/" + id + "/review?ok=reviewed"
	if res.Points > 0 {
		target += "&p=" + strconv.Itoa(res.Points)
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// handleExtensionPage 处理补交表单（仅教师）。
func (s *Server) handleExtensionPage(w http.ResponseWriter, r *http.Request) {
	u, ok := s.pageUser(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	back := "/assignments/" + id + "/review"
	if err := r.ParseForm(); err != nil {
		s.renderPageError(w, r, apperr.Invalid("表单格式错误"), back)
		return
	}
	deadline, err := parseFormDeadline(r.PostFormValue("deadline_at"))
	if err != nil {
		s.renderPageError(w, r, err, back)
		return
	}
	if _, err := s.extensions.Grant(r.Context(), u, id, service.ExtensionInput{
		StudentID:  r.PostFormValue("student_id"),
		DeadlineAt: deadline,
		Reason:     r.PostFormValue("reason"),
	}); err != nil {
		s.renderPageError(w, r, err, back)
		return
	}
	http.Redirect(w, r, back+"?ok=extended", http.StatusSeeOther)
}

type studentSummaryRow struct {
	StudentID    string
	Name         string
	State        string
	StateLabel   string
	VersionNo    int
	VersionCount int
	FirstSubmit  string
	LastSubmit   string
	Points       int
	Overdue      bool
	Extension    *store.Extension
	Decision     string
	Feedback     string
}

type summaryPageData struct {
	Base           pageBase
	Assignment     store.Assignment
	Title          string
	Deadline       string
	ReadOnly       bool
	Counts         map[string]int
	CompletionRate string
	Students       []studentSummaryRow
	Points         []store.PointsEntry
	TotalPoints    int
	Timeline       []timelineRow
}

type timelineRow struct {
	Time       string
	StudentID  string
	Type       string
	ActorRole  string
	Detail     string
	Transition string
}

// handleSummaryPage 是班级汇总页：教师看明细与流水；助教只读统计。
func (s *Server) handleSummaryPage(w http.ResponseWriter, r *http.Request) {
	u, ok := s.pageUser(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	summary, err := s.overview.Summary(r.Context(), u, id)
	if err != nil {
		s.renderPageError(w, r, err, "/assignments")
		return
	}

	data := summaryPageData{
		Base:           s.baseData(u),
		Assignment:     summary.Assignment,
		Title:          summary.Assignment.EffectiveTitle(),
		Deadline:       summary.Assignment.EffectiveDeadline().Format("2006-01-02 15:04"),
		ReadOnly:       summary.ReadOnlyStats,
		Counts:         summary.Counts,
		CompletionRate: strconv.FormatFloat(summary.CompletionRate*100, 'f', 0, 64) + "%",
		Points:         summary.Points,
		TotalPoints:    summary.TotalPoints,
	}
	for _, ev := range summary.Timeline {
		row := timelineRow{
			Time:      formatPageTime(ev.CreatedAt),
			StudentID: ev.StudentID,
			Type:      labelOf(eventTypeLabels, ev.Type),
			ActorRole: roleLabel(ev.ActorRole),
			Detail:    ev.Detail,
		}
		if ev.FromState != "" || ev.ToState != "" {
			row.Transition = labelOf(stateLabels, ev.FromState) + " → " + labelOf(stateLabels, ev.ToState)
		}
		data.Timeline = append(data.Timeline, row)
	}
	for _, st := range summary.Students {
		row := studentSummaryRow{
			StudentID:    st.StudentID,
			Name:         st.Name,
			State:        st.State,
			StateLabel:   labelOf(stateLabels, st.State),
			VersionNo:    st.CurrentVersionNo,
			VersionCount: st.VersionCount,
			FirstSubmit:  formatPageTime(st.FirstSubmittedAt),
			LastSubmit:   formatPageTime(st.LastSubmittedAt),
			Points:       st.Points,
			Overdue:      st.Overdue,
			Extension:    st.Extension,
		}
		if st.LatestReview != nil {
			row.Decision = labelOf(decisionLabels, st.LatestReview.Decision)
			row.Feedback = st.LatestReview.Feedback
		}
		data.Students = append(data.Students, row)
	}
	data.Base.OK = okMessage(r)
	s.renderPage(w, http.StatusOK, "summary.html", data)
}

type boardStudentRow struct {
	StudentID         string
	Name              string
	State             string
	StateLabel        string
	CurrentVersionNo  int
	EffectiveDeadline string
	LastSubmitted     string
	ExtensionApplied  bool
	Overdue           bool
	ReminderCount     int
	LastReminder      string
}

type reminderRow struct {
	Time         string
	StudentID    string
	AssistantID  string
	ChannelLabel string
	Note         string
}

type boardPageData struct {
	Base           pageBase
	Assignment     store.Assignment
	Title          string
	Deadline       string
	Counts         map[string]int
	CompletionRate string
	Students       []boardStudentRow
	Reminders      []reminderRow
	ReminderCount  int
	CanRemind      bool
	DefaultTime    string
}

// handleBoardPage 是助教看板：四类清单 + 催交；教师可看但只读。
func (s *Server) handleBoardPage(w http.ResponseWriter, r *http.Request) {
	u, ok := s.pageUser(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	board, err := s.overview.Board(r.Context(), u, id)
	if err != nil {
		s.renderPageError(w, r, err, "/assignments")
		return
	}
	data := boardPageData{
		Base:           s.baseData(u),
		Assignment:     board.Assignment,
		Title:          board.Assignment.EffectiveTitle(),
		Deadline:       board.Assignment.EffectiveDeadline().Format("2006-01-02 15:04"),
		Counts:         board.Counts,
		CompletionRate: strconv.FormatFloat(board.CompletionRate*100, 'f', 0, 64) + "%",
		ReminderCount:  len(board.Reminders),
		CanRemind:      u.Role == store.RoleAssistant,
	}
	for _, st := range board.Students {
		data.Students = append(data.Students, boardStudentRow{
			StudentID:         st.StudentID,
			Name:              st.Name,
			State:             st.State,
			StateLabel:        labelOf(stateLabels, st.State),
			CurrentVersionNo:  st.CurrentVersionNo,
			EffectiveDeadline: formatPageTime(st.EffectiveDeadline),
			LastSubmitted:     formatPageTime(st.LastSubmittedAt),
			ExtensionApplied:  st.ExtensionApplied,
			Overdue:           st.Overdue,
			ReminderCount:     st.ReminderCount,
			LastReminder:      formatPageTime(st.LastReminderAt),
		})
	}
	for _, rem := range board.Reminders {
		data.Reminders = append(data.Reminders, reminderRow{
			Time:         formatPageTime(rem.CreatedAt),
			StudentID:    rem.StudentID,
			AssistantID:  rem.AssistantID,
			ChannelLabel: labelOf(channelLabels, rem.Channel),
			Note:         rem.Note,
		})
	}
	data.Base.OK = okMessage(r)
	s.renderPage(w, http.StatusOK, "board.html", data)
}

// handleReminderPage 处理助教催交表单。
func (s *Server) handleReminderPage(w http.ResponseWriter, r *http.Request) {
	u, ok := s.pageUser(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	back := "/assignments/" + id + "/board"
	if err := r.ParseForm(); err != nil {
		s.renderPageError(w, r, apperr.Invalid("表单格式错误"), back)
		return
	}
	if _, err := s.overview.RecordReminder(r.Context(), u, id, service.ReminderInput{
		StudentID: r.PostFormValue("student_id"),
		Channel:   r.PostFormValue("channel"),
		Note:      r.PostFormValue("note"),
	}); err != nil {
		s.renderPageError(w, r, err, back)
		return
	}
	http.Redirect(w, r, back+"?ok=reminded", http.StatusSeeOther)
}

// assignmentForPage 页面侧的作业读取：跨班与不存在统一 404，与接口口径一致。
func (s *Server) assignmentForPage(r *http.Request, u store.User, id string) (store.Assignment, error) {
	a, err := s.store.GetAssignment(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.Assignment{}, apperr.NotFound("作业不存在")
		}
		return store.Assignment{}, err
	}
	if a.ClassID != u.ClassID {
		return store.Assignment{}, apperr.NotFound("作业不存在")
	}
	return a, nil
}

func formatPageTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02 15:04")
}

// parseFormDeadline 接受浏览器 datetime-local 与本机时区；带时区的 RFC3339 也接受。
func parseFormDeadline(v string) (time.Time, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return time.Time{}, apperr.Invalid("截止时间不能为空")
	}
	if t, err := time.ParseInLocation("2006-01-02T15:04", v, time.Local); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, nil
	}
	return time.Time{}, apperr.Invalid("截止时间格式不正确，应为 YYYY-MM-DDTHH:MM")
}

func newPageRequestID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(buf)
}
