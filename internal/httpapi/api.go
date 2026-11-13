package httpapi

import (
	"errors"
	"net/http"

	"onlineclass/internal/store"
)

// handleMe 返回当前身份与可执行动作（用于演示"身份只来自会话"）。
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	u, _ := userFrom(r)
	actions := []Action{ActionViewAssignment, ActionSubmit, ActionReview, ActionGrantExtension, ActionRecordReminder, ActionViewStudent}
	allowed := make([]string, 0, len(actions))
	for _, a := range actions {
		if Allow(u.Role, a, true, true) {
			allowed = append(allowed, string(a))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":          u.ID,
		"name":        u.Name,
		"role":        u.Role,
		"class_id":    u.ClassID,
		"permissions": allowed,
	})
}

// assignmentFor 载入路径中的作业并做班级隔离。
//
// 不存在与跨班都返回 404：避免通过状态码差异判断作业是否存在。
func (s *Server) assignmentFor(w http.ResponseWriter, r *http.Request) (store.Assignment, store.User, bool) {
	u, ok := userFrom(r)
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, "unauthenticated", "请先登录")
		return store.Assignment{}, store.User{}, false
	}
	a, err := s.store.GetAssignment(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeAPIError(w, http.StatusNotFound, "not_found", "作业不存在")
			return store.Assignment{}, store.User{}, false
		}
		logError("查询作业", err)
		writeAPIError(w, http.StatusInternalServerError, "internal", "服务内部错误")
		return store.Assignment{}, store.User{}, false
	}
	if a.ClassID != u.ClassID {
		writeAPIError(w, http.StatusNotFound, "not_found", "作业不存在")
		return store.Assignment{}, store.User{}, false
	}
	return a, u, true
}

// handleAssignmentGet 返回作业的对外视图（已发布以快照为准）。
func (s *Server) handleAssignmentGet(w http.ResponseWriter, r *http.Request) {
	a, u, ok := s.assignmentFor(w, r)
	if !ok {
		return
	}
	if !Allow(u.Role, ActionViewAssignment, true, false) {
		writeAPIError(w, http.StatusForbidden, "forbidden_role", "当前角色无权查看作业")
		return
	}
	// 草稿仅本班教师可见：对学生与助教按"不存在"处理，避免提前泄露未发布内容。
	if !a.Published() && u.Role != store.RoleTeacher {
		writeAPIError(w, http.StatusNotFound, "not_found", "作业不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":           a.ID,
		"class_id":     a.ClassID,
		"state":        a.State,
		"title":        a.EffectiveTitle(),
		"requirement":  a.EffectiveRequirement(),
		"deadline_at":  a.EffectiveDeadline().Format("2006-01-02T15:04:05Z07:00"),
		"published_at": a.PublishedAt.Format("2006-01-02T15:04:05Z07:00"),
		"can_submit":   Allow(u.Role, ActionSubmit, true, true),
		"can_review":   Allow(u.Role, ActionReview, true, false),
	})
}

// handleStudentGet 返回学生基础状态；学生只能看自己，助教/教师看同班。
func (s *Server) handleStudentGet(w http.ResponseWriter, r *http.Request) {
	u, _ := userFrom(r)
	target, err := s.store.GetUser(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeAPIError(w, http.StatusNotFound, "not_found", "学生不存在")
			return
		}
		logError("查询学生", err)
		writeAPIError(w, http.StatusInternalServerError, "internal", "服务内部错误")
		return
	}
	if target.ClassID != u.ClassID {
		writeAPIError(w, http.StatusNotFound, "not_found", "学生不存在")
		return
	}
	if !Allow(u.Role, ActionViewStudent, true, target.ID == u.ID) {
		writeAPIError(w, http.StatusForbidden, "forbidden_role", "无权查看该学生数据")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":       target.ID,
		"name":     target.Name,
		"role":     target.Role,
		"class_id": target.ClassID,
	})
}

// 催交接口在 D-03 只做权限校验，业务逻辑由 D-10 补齐；
// 通过权限后返回 501，便于权限矩阵测试与页面联调区分"无权"与"未实现"。

func (s *Server) handleReminderPost(w http.ResponseWriter, r *http.Request) {
	_, u, ok := s.assignmentFor(w, r)
	if !ok {
		return
	}
	if !Allow(u.Role, ActionRecordReminder, true, false) {
		writeAPIError(w, http.StatusForbidden, "forbidden_role", "只有本班助教可以记录催交")
		return
	}
	writeAPIError(w, http.StatusNotImplemented, "not_implemented", "催交能力将在 D-10 实现，当前仅完成权限校验")
}
