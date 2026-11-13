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

type reviewPayload struct {
	VersionID string `json:"version_id"`
	Decision  string `json:"decision"`
	Feedback  string `json:"feedback"`
	RequestID string `json:"request_id"`
}

func reviewView(rev store.Review) map[string]any {
	view := map[string]any{
		"id":         rev.ID,
		"version_id": rev.VersionID,
		"decision":   rev.Decision,
		"feedback":   rev.Feedback,
		"teacher_id": rev.TeacherID,
		"created_at": rev.CreatedAt.Format(time.RFC3339),
	}
	if rev.RequestID != "" {
		view["request_id"] = rev.RequestID
	}
	return view
}

// handleReviewPost 教师批阅指定版本：通过 → PASSED，退回 → RETURNED。
func (s *Server) handleReviewPost(w http.ResponseWriter, r *http.Request) {
	u, _ := userFrom(r)
	if u.Role != store.RoleTeacher {
		writeAPIError(w, http.StatusForbidden, "forbidden_role", "只有教师可以批阅")
		return
	}
	if _, _, ok := s.assignmentFor(w, r); !ok {
		return
	}

	var p reviewPayload
	dec := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		writeServiceError(w, apperr.Invalid("请求体格式错误，需要 JSON：version_id、decision、feedback"))
		return
	}

	res, err := s.reviews.Review(r.Context(), u, r.PathValue("id"), service.ReviewInput{
		VersionID: p.VersionID,
		Decision:  p.Decision,
		Feedback:  p.Feedback,
		RequestID: p.RequestID,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}

	body := reviewView(res.Review)
	body["state"] = res.Enrollment.State
	body["current_version_no"] = res.Enrollment.CurrentVersionNo
	body["replayed"] = res.Replayed
	body["points_awarded"] = res.PointsAwarded
	body["points"] = res.Points
	status := http.StatusCreated
	if res.Replayed {
		status = http.StatusOK
	}
	writeJSON(w, status, body)
}
