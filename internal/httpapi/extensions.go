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

type extensionPayload struct {
	StudentID  string `json:"student_id"`
	DeadlineAt string `json:"deadline_at"`
	Reason     string `json:"reason"`
}

func extensionView(ext store.Extension) map[string]any {
	return map[string]any{
		"student_id":  ext.StudentID,
		"deadline_at": ext.DeadlineAt.Format(time.RFC3339),
		"reason":      ext.Reason,
		"granted_by":  ext.GrantedBy,
		"granted_at":  ext.GrantedAt.Format(time.RFC3339),
	}
}

// handleExtensionPost 教师为指定学生设置一次性补交截止。
func (s *Server) handleExtensionPost(w http.ResponseWriter, r *http.Request) {
	u, _ := userFrom(r)
	if u.Role != store.RoleTeacher {
		writeAPIError(w, http.StatusForbidden, "forbidden_role", "只有教师可以批准补交")
		return
	}
	if _, _, ok := s.assignmentFor(w, r); !ok {
		return
	}

	var p extensionPayload
	dec := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		writeServiceError(w, apperr.Invalid("请求体格式错误，需要 JSON：student_id、deadline_at、reason"))
		return
	}
	deadline, err := time.Parse(time.RFC3339, p.DeadlineAt)
	if err != nil {
		writeServiceError(w, apperr.Invalid("deadline_at 需要 RFC3339 时间，例如 2026-12-01T10:00:00Z"))
		return
	}

	ext, err := s.extensions.Grant(r.Context(), u, r.PathValue("id"), service.ExtensionInput{
		StudentID:  p.StudentID,
		DeadlineAt: deadline,
		Reason:     p.Reason,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, extensionView(ext))
}
