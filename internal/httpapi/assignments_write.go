package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"onlineclass/internal/apperr"
	"onlineclass/internal/service"
	"onlineclass/internal/store"
)

// assignmentPayload 是创建/修改草稿的请求体。
type assignmentPayload struct {
	Title       string `json:"title"`
	Requirement string `json:"requirement"`
	DeadlineAt  string `json:"deadline_at"`
}

func (p assignmentPayload) toInput() (service.AssignmentInput, error) {
	in := service.AssignmentInput{Title: p.Title, Requirement: p.Requirement}
	if p.DeadlineAt == "" {
		return in, nil
	}
	t, err := time.Parse(time.RFC3339, p.DeadlineAt)
	if err != nil {
		return in, apperr.Invalid("deadline_at 必须是 RFC3339 时间，例如 2026-12-01T23:59:00+08:00")
	}
	in.DeadlineAt = t
	return in, nil
}

// decodeAssignment 解析请求体；未知字段视为参数错误，避免前端拼错字段被静默忽略。
func decodeAssignment(r *http.Request) (service.AssignmentInput, error) {
	var p assignmentPayload
	dec := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return service.AssignmentInput{}, apperr.Invalid("请求体格式错误，需要 JSON：title、requirement、deadline_at")
	}
	return p.toInput()
}

func assignmentView(a store.Assignment) map[string]any {
	v := map[string]any{
		"id":          a.ID,
		"class_id":    a.ClassID,
		"state":       a.State,
		"title":       a.EffectiveTitle(),
		"requirement": a.EffectiveRequirement(),
		"deadline_at": a.EffectiveDeadline().Format(time.RFC3339),
		"snapshot_by": a.SnapshotBy,
	}
	if !a.PublishedAt.IsZero() {
		v["published_at"] = a.PublishedAt.Format(time.RFC3339)
	}
	if !a.SnapshotAt.IsZero() {
		v["snapshot_at"] = a.SnapshotAt.Format(time.RFC3339)
	}
	return v
}

// handleAssignmentCreate 创建草稿（教师）。
func (s *Server) handleAssignmentCreate(w http.ResponseWriter, r *http.Request) {
	u, _ := userFrom(r)
	if u.Role != store.RoleTeacher {
		writeAPIError(w, http.StatusForbidden, "forbidden_role", "只有教师可以创建作业")
		return
	}
	in, err := decodeAssignment(r)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	a, err := s.assignments.Create(r.Context(), u, in)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, assignmentView(a))
}

// handleAssignmentUpdate 修改草稿；已发布返回 409 assignment_locked。
func (s *Server) handleAssignmentUpdate(w http.ResponseWriter, r *http.Request) {
	u, _ := userFrom(r)
	if u.Role != store.RoleTeacher {
		writeAPIError(w, http.StatusForbidden, "forbidden_role", "只有教师可以修改作业")
		return
	}
	in, err := decodeAssignment(r)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	a, err := s.assignments.UpdateDraft(r.Context(), u, r.PathValue("id"), in)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, assignmentView(a))
}

// handleAssignmentPublish 发布并把内容固化为快照。
func (s *Server) handleAssignmentPublish(w http.ResponseWriter, r *http.Request) {
	u, _ := userFrom(r)
	if u.Role != store.RoleTeacher {
		writeAPIError(w, http.StatusForbidden, "forbidden_role", "只有教师可以发布作业")
		return
	}
	a, err := s.assignments.Publish(r.Context(), u, r.PathValue("id"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, assignmentView(a))
}

// writeServiceError 把业务错误映射为响应；未识别的错误按 500 处理并记录日志。
func writeServiceError(w http.ResponseWriter, err error) {
	var ae *apperr.Error
	if errors.As(err, &ae) {
		body := map[string]any{"code": ae.Code, "message": ae.Message}
		if len(ae.Details) > 0 {
			body["details"] = ae.Details
		}
		writeJSON(w, ae.Status, body)
		return
	}
	logError("业务处理", err)
	writeAPIError(w, http.StatusInternalServerError, "internal", "服务内部错误")
}
