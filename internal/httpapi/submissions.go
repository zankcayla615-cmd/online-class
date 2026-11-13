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

// submissionPayload 是学生提交的请求体。
type submissionPayload struct {
	Content     string `json:"content"`
	RequestID   string `json:"request_id"`
	BaseVersion *int   `json:"base_version"`
}

func submissionView(sub store.Submission) map[string]any {
	return map[string]any{
		"id":           sub.ID,
		"version_no":   sub.VersionNo,
		"content":      sub.Content,
		"content_hash": sub.ContentHash,
		"request_id":   sub.RequestID,
		"created_at":   sub.CreatedAt.Format(time.RFC3339),
	}
}

// handleSubmissionPost 学生提交或更新提交（追加新版本）。
func (s *Server) handleSubmissionPost(w http.ResponseWriter, r *http.Request) {
	u, _ := userFrom(r)
	if u.Role != store.RoleStudent {
		writeAPIError(w, http.StatusForbidden, "forbidden_role", "只有学生可以提交作业")
		return
	}
	// 先做资源解析与班级隔离（跨班 404），再校验请求体：
	// 否则"外班 + 空请求体"会先暴露 400，等同于泄露作业存在性。
	if _, _, ok := s.assignmentFor(w, r); !ok {
		return
	}

	var p submissionPayload
	dec := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		writeServiceError(w, apperr.Invalid("请求体格式错误，需要 JSON：content、request_id"))
		return
	}

	res, err := s.submissions.Submit(r.Context(), u, r.PathValue("id"), service.SubmitInput{
		Content:     p.Content,
		RequestID:   p.RequestID,
		BaseVersion: p.BaseVersion,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}

	body := submissionView(res.Version)
	body["state"] = res.Enrollment.State
	body["current_version_no"] = res.Enrollment.CurrentVersionNo
	body["effective_deadline"] = res.EffectiveDeadline.Format(time.RFC3339)
	body["extension_applied"] = res.ExtensionApplied
	body["replayed"] = res.Replayed
	// 重放不产生新资源，按 200 返回；新建版本仍是 201。
	status := http.StatusCreated
	if res.Replayed {
		status = http.StatusOK
	}
	writeJSON(w, status, body)
}

// handleSubmissionList 返回某学生在该作业上的全部版本（含历史版本）。
//
// 学生默认查自己且只能查自己；教师/助教需指定 ?student_id= 且限本班。
func (s *Server) handleSubmissionList(w http.ResponseWriter, r *http.Request) {
	u, _ := userFrom(r)

	studentID := r.URL.Query().Get("student_id")
	if u.Role == store.RoleStudent && studentID != "" && studentID != u.ID {
		writeAPIError(w, http.StatusForbidden, "forbidden_role", "学生只能查看自己的提交")
		return
	}

	h, err := s.submissions.History(r.Context(), u, r.PathValue("id"), studentID)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	versions := make([]map[string]any, 0, len(h.Versions))
	for _, sub := range h.Versions {
		view := submissionView(sub)
		if rev, ok := h.Reviews[sub.VersionNo]; ok {
			view["review"] = reviewView(rev)
		}
		versions = append(versions, view)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"assignment_id":      h.AssignmentID,
		"student_id":         h.StudentID,
		"state":              h.State,
		"current_version_no": h.CurrentVersionNo,
		"effective_deadline": h.EffectiveDeadline.Format(time.RFC3339),
		"extension_applied":  h.ExtensionApplied,
		"versions":           versions,
	})
}
