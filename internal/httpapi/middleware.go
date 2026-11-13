package httpapi

import (
	"context"
	"net/http"

	"onlineclass/internal/store"
)

type ctxKey int

const userCtxKey ctxKey = iota

// withUser 从会话解析当前用户并放入请求上下文。
//
// 身份只来自服务端会话：请求体、查询参数或请求头中的用户编号一律不采信，
// 因此"改参数冒充他人"无法越权。
func (s *Server) withUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}
		sess, ok := s.sessions.Get(cookie.Value)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		u, err := s.store.GetUser(r.Context(), sess.UserID)
		if err != nil {
			// 账号被删除或会话指向无效用户：按未登录处理。
			next.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userCtxKey, u)))
	})
}

// userFrom 返回上下文中的当前用户。
func userFrom(r *http.Request) (store.User, bool) {
	u, ok := r.Context().Value(userCtxKey).(store.User)
	return u, ok
}

// requireAuth 拦截未登录请求，统一返回 401 JSON（页面路由不走它）。
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := userFrom(r); !ok {
			writeAPIError(w, http.StatusUnauthorized, "unauthenticated", "请先登录")
			return
		}
		next.ServeHTTP(w, r)
	})
}
