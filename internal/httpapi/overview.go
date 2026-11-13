package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"onlineclass/internal/apperr"
	"onlineclass/internal/service"
	"onlineclass/internal/store"
)

func studentStatusView(st service.StudentStatus) map[string]any {
	view := map[string]any{
		"student_id":         st.StudentID,
		"name":               st.Name,
		"state":              st.State,
		"current_version_no": st.CurrentVersionNo,
		"effective_deadline": st.EffectiveDeadline.Format(time.RFC3339),
		"extension_applied":  st.ExtensionApplied,
		"overdue":            st.Overdue,
		"reminder_count":     st.ReminderCount,
	}
	if !st.LastSubmittedAt.IsZero() {
		view["last_submitted_at"] = st.LastSubmittedAt.Format(time.RFC3339)
	}
	if !st.LastReminderAt.IsZero() {
		view["last_reminder_at"] = st.LastReminderAt.Format(time.RFC3339)
	}
	return view
}

func reminderView(r store.Reminder) map[string]any {
	return map[string]any{
		"id":           r.ID,
		"student_id":   r.StudentID,
		"assistant_id": r.AssistantID,
		"channel":      r.Channel,
		"note":         r.Note,
		"created_at":   r.CreatedAt.Format(time.RFC3339),
	}
}

func eventView(e store.Event) map[string]any {
	view := map[string]any{
		"type":       e.Type,
		"student_id": e.StudentID,
		"actor_id":   e.ActorID,
		"actor_role": e.ActorRole,
		"detail":     e.Detail,
		"created_at": e.CreatedAt.Format(time.RFC3339),
	}
	if e.FromState != "" {
		view["from_state"] = e.FromState
	}
	if e.ToState != "" {
		view["to_state"] = e.ToState
	}
	if e.VersionNo != nil {
		view["version_no"] = *e.VersionNo
	}
	return view
}

func pointsEntryView(e store.PointsEntry) map[string]any {
	return map[string]any{
		"id":            e.ID,
		"student_id":    e.StudentID,
		"assignment_id": e.AssignmentID,
		"reason":        e.Reason,
		"points":        e.Points,
		"created_at":    e.CreatedAt.Format(time.RFC3339),
	}
}

// handleBoardGet 返回四类清单与催交记录（教师、助教；不含正文）。
func (s *Server) handleBoardGet(w http.ResponseWriter, r *http.Request) {
	u, _ := userFrom(r)
	board, err := s.overview.Board(r.Context(), u, r.PathValue("id"))
	if err != nil {
		writeServiceError(w, err)
		return
	}

	students := make([]map[string]any, 0, len(board.Students))
	for _, st := range board.Students {
		students = append(students, studentStatusView(st))
	}
	reminders := make([]map[string]any, 0, len(board.Reminders))
	for _, rem := range board.Reminders {
		reminders = append(reminders, reminderView(rem))
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"assignment_id":   board.Assignment.ID,
		"title":           board.Assignment.EffectiveTitle(),
		"deadline_at":     board.Assignment.EffectiveDeadline().Format(time.RFC3339),
		"generated_at":    board.GeneratedAt.Format(time.RFC3339),
		"counts":          board.Counts,
		"completion_rate": board.CompletionRate,
		"students":        students,
		"reminders":       reminders,
	})
}

// handleSummaryGet 返回班级汇总。助教只拿到统计（无人均明细/流水/时间线）。
func (s *Server) handleSummaryGet(w http.ResponseWriter, r *http.Request) {
	u, _ := userFrom(r)
	summary, err := s.overview.Summary(r.Context(), u, r.PathValue("id"))
	if err != nil {
		writeServiceError(w, err)
		return
	}

	body := map[string]any{
		"assignment_id":   summary.Assignment.ID,
		"title":           summary.Assignment.EffectiveTitle(),
		"deadline_at":     summary.Assignment.EffectiveDeadline().Format(time.RFC3339),
		"generated_at":    summary.GeneratedAt.Format(time.RFC3339),
		"read_only_stats": summary.ReadOnlyStats,
		"counts":          summary.Counts,
		"completion_rate": summary.CompletionRate,
	}
	if summary.ReadOnlyStats {
		writeJSON(w, http.StatusOK, body)
		return
	}

	students := make([]map[string]any, 0, len(summary.Students))
	for _, st := range summary.Students {
		view := studentStatusView(st.StudentStatus)
		view["version_count"] = st.VersionCount
		view["points"] = st.Points
		if !st.FirstSubmittedAt.IsZero() {
			view["first_submitted_at"] = st.FirstSubmittedAt.Format(time.RFC3339)
		}
		if st.Extension != nil {
			view["extension"] = map[string]any{
				"deadline_at": st.Extension.DeadlineAt.Format(time.RFC3339),
				"reason":      st.Extension.Reason,
				"granted_by":  st.Extension.GrantedBy,
				"granted_at":  st.Extension.GrantedAt.Format(time.RFC3339),
			}
		}
		if st.LatestReview != nil {
			view["latest_review"] = map[string]any{
				"decision":   st.LatestReview.Decision,
				"feedback":   st.LatestReview.Feedback,
				"created_at": st.LatestReview.CreatedAt.Format(time.RFC3339),
			}
		}
		students = append(students, view)
	}

	points := make([]map[string]any, 0, len(summary.Points))
	for _, e := range summary.Points {
		points = append(points, pointsEntryView(e))
	}
	timeline := make([]map[string]any, 0, len(summary.Timeline))
	for _, e := range summary.Timeline {
		timeline = append(timeline, eventView(e))
	}

	body["students"] = students
	body["points"] = points
	body["total_points"] = summary.TotalPoints
	body["timeline"] = timeline
	writeJSON(w, http.StatusOK, body)
}

type reminderPayload struct {
	StudentID string `json:"student_id"`
	Channel   string `json:"channel"`
	Note      string `json:"note"`
}

// handleReminderPost 追加一条催交记录（只追加，不提供修改/删除接口）。
func (s *Server) handleReminderPost(w http.ResponseWriter, r *http.Request) {
	u, _ := userFrom(r)
	// 先按角色拒绝：越权请求不该因为请求体格式而变成 400。
	if u.Role != store.RoleAssistant {
		writeAPIError(w, http.StatusForbidden, "forbidden_role", "只有助教可以记录催交")
		return
	}
	var p reminderPayload
	dec := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		writeServiceError(w, apperr.Invalid("请求体格式错误，需要 JSON：student_id、channel、note"))
		return
	}

	rem, err := s.overview.RecordReminder(r.Context(), u, r.PathValue("id"), service.ReminderInput{
		StudentID: p.StudentID,
		Channel:   p.Channel,
		Note:      p.Note,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, reminderView(rem))
}
