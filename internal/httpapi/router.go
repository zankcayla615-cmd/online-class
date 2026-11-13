// Package httpapi 提供 HTTP 接口与页面。
//
// 约定：
//   - 身份只来自会话（见 middleware.go）；
//   - 跨班资源统一返回 404，不泄露存在性；
//   - 业务拒绝返回 4xx + 稳定错误码，不返回 500。
package httpapi

import (
	"embed"
	"html/template"
	"log"
	"net/http"
	"time"

	"onlineclass/internal/clock"
	"onlineclass/internal/service"
	"onlineclass/internal/session"
	"onlineclass/internal/store"
)

//go:embed templates/*.html
var templateFS embed.FS

const sessionCookieName = "oc_session"

// Server 汇总处理请求所需的依赖。
type Server struct {
	store       *store.Store
	clk         *clock.Clock
	sessions    *session.Manager
	tpl         *template.Template
	assignments *service.AssignmentService
	submissions *service.SubmissionService
	reviews     *service.ReviewService
	extensions  *service.ExtensionService
	overview    *service.OverviewService
}

// New 构造服务；ttl 为会话有效期。
func New(st *store.Store, clk *clock.Clock, ttl time.Duration) *Server {
	tpl := template.Must(template.ParseFS(templateFS, "templates/*.html"))
	return &Server{
		store:       st,
		clk:         clk,
		sessions:    session.NewManager(clk, ttl),
		tpl:         tpl,
		assignments: service.NewAssignmentService(st, clk),
		submissions: service.NewSubmissionService(st, clk),
		reviews:     service.NewReviewService(st, clk),
		extensions:  service.NewExtensionService(st, clk),
		overview:    service.NewOverviewService(st, clk),
	}
}

// Handler 注册路由。所有请求先经过身份解析中间件。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", s.handleHealth)

	mux.HandleFunc("GET /login", s.handleLoginPage)
	mux.HandleFunc("POST /login", s.handleLogin)
	mux.HandleFunc("POST /logout", s.handleLogout)
	mux.HandleFunc("GET /{$}", s.handleRoot)

	// 页面路由：服务端渲染，表单提交走 PRG，规则仍由 service 判定。
	mux.HandleFunc("GET /assignments", s.handleAssignmentsPage)
	mux.HandleFunc("POST /assignments", s.handleAssignmentCreatePage)
	mux.HandleFunc("POST /assignments/{id}", s.handleAssignmentUpdatePage)
	mux.HandleFunc("POST /assignments/{id}/publish", s.handleAssignmentPublishPage)
	mux.HandleFunc("GET /assignments/{id}", s.handleStudentAssignmentPage)
	mux.HandleFunc("POST /assignments/{id}/submit", s.handleSubmitPage)
	mux.HandleFunc("GET /assignments/{id}/review", s.handleReviewPage)
	mux.HandleFunc("POST /assignments/{id}/review", s.handleReviewSubmitPage)
	mux.HandleFunc("POST /assignments/{id}/extension", s.handleExtensionPage)
	mux.HandleFunc("GET /assignments/{id}/summary", s.handleSummaryPage)
	mux.HandleFunc("GET /assignments/{id}/board", s.handleBoardPage)
	mux.HandleFunc("POST /assignments/{id}/reminder", s.handleReminderPage)

	mux.Handle("GET /api/me", s.requireAuth(http.HandlerFunc(s.handleMe)))
	mux.Handle("POST /api/assignments", s.requireAuth(http.HandlerFunc(s.handleAssignmentCreate)))
	mux.Handle("PATCH /api/assignments/{id}", s.requireAuth(http.HandlerFunc(s.handleAssignmentUpdate)))
	mux.Handle("POST /api/assignments/{id}/publish", s.requireAuth(http.HandlerFunc(s.handleAssignmentPublish)))
	mux.Handle("GET /api/assignments/{id}", s.requireAuth(http.HandlerFunc(s.handleAssignmentGet)))
	mux.Handle("GET /api/students/{id}", s.requireAuth(http.HandlerFunc(s.handleStudentGet)))
	mux.Handle("POST /api/assignments/{id}/submissions", s.requireAuth(http.HandlerFunc(s.handleSubmissionPost)))
	mux.Handle("GET /api/assignments/{id}/submissions", s.requireAuth(http.HandlerFunc(s.handleSubmissionList)))
	mux.Handle("POST /api/assignments/{id}/reviews", s.requireAuth(http.HandlerFunc(s.handleReviewPost)))
	mux.Handle("POST /api/assignments/{id}/extensions", s.requireAuth(http.HandlerFunc(s.handleExtensionPost)))
	mux.Handle("GET /api/assignments/{id}/board", s.requireAuth(http.HandlerFunc(s.handleBoardGet)))
	mux.Handle("GET /api/assignments/{id}/summary", s.requireAuth(http.HandlerFunc(s.handleSummaryGet)))
	mux.Handle("POST /api/assignments/{id}/reminders", s.requireAuth(http.HandlerFunc(s.handleReminderPost)))

	return s.withUser(mux)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status": "ok",
		"now":    s.clk.Now().Format(time.RFC3339),
		"offset": s.clk.Offset().String(),
	})
}

// logError 记录服务端内部错误（不把内部细节返回给客户端）。
func logError(action string, err error) {
	log.Printf("[httpapi] %s 失败: %v", action, err)
}
