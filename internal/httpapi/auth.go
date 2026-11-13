package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"onlineclass/internal/auth"
	"onlineclass/internal/store"
)

// handleLoginPage 渲染登录页；已登录直接回首页。
func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := userFrom(r); ok {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.renderLogin(w, http.StatusOK, "", "")
}

// handleLogin 校验账号口令并建立会话。
//
// 失败一律返回同一条提示，避免通过错误信息探测账号是否存在。
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderLogin(w, http.StatusBadRequest, "表单格式错误", "")
		return
	}
	userID := strings.TrimSpace(r.PostFormValue("user_id"))
	password := r.PostFormValue("password")
	if userID == "" || password == "" {
		s.renderLogin(w, http.StatusBadRequest, "请输入账号与口令", userID)
		return
	}

	u, err := s.store.GetUser(r.Context(), userID)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			logError("查询账号", err)
		}
		s.renderLogin(w, http.StatusUnauthorized, "账号或口令不正确", userID)
		return
	}
	if !auth.VerifyPassword(u.PasswordHash, password) {
		s.renderLogin(w, http.StatusUnauthorized, "账号或口令不正确", userID)
		return
	}

	sid, err := s.sessions.Create(u.ID)
	if err != nil {
		logError("创建会话", err)
		http.Error(w, "服务内部错误", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    sid,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int((8 * time.Hour).Seconds()),
		// 教学演示走 http，故不设 Secure；部署到 https 时应开启。
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// handleLogout 销毁会话并清除 Cookie。
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookieName); err == nil {
		s.sessions.Destroy(c.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) renderLogin(w http.ResponseWriter, status int, errMsg, userID string) {
	data := map[string]any{"Error": errMsg, "UserID": userID}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := s.tpl.ExecuteTemplate(w, "login.html", data); err != nil {
		logError("渲染登录页", err)
	}
}

// handleHome 是登录后的临时首页（D-11 替换为正式页面）：
// 展示当前身份与该角色可执行的动作，用于演示权限边界。
func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	u, ok := userFrom(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	actions := []Action{ActionViewAssignment, ActionSubmit, ActionReview, ActionGrantExtension, ActionRecordReminder, ActionViewStudent}
	var allowed []string
	for _, a := range actions {
		if Allow(u.Role, a, true, true) {
			allowed = append(allowed, actionLabel(a))
		}
	}
	data := map[string]any{
		"User":        u,
		"Role":        roleLabel(u.Role),
		"Allowed":     allowed,
		"Now":         s.clk.Now().Format("2006-01-02 15:04 MST"),
		"Offset":      s.clk.Offset().String(),
		"StudentView": u.Role == store.RoleStudent,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tpl.ExecuteTemplate(w, "home.html", data); err != nil {
		logError("渲染首页", err)
	}
}

func roleLabel(role string) string {
	switch role {
	case store.RoleTeacher:
		return "教师"
	case store.RoleAssistant:
		return "助教"
	case store.RoleStudent:
		return "学生"
	}
	return role
}

func actionLabel(a Action) string {
	switch a {
	case ActionViewAssignment:
		return "查看本班作业"
	case ActionSubmit:
		return "提交/更新自己的作业"
	case ActionReview:
		return "批阅作业"
	case ActionGrantExtension:
		return "批准补交"
	case ActionRecordReminder:
		return "记录催交"
	case ActionViewStudent:
		return "查看学生状态"
	}
	return string(a)
}
