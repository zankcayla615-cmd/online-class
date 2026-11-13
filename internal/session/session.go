// Package session 提供服务端会话。
//
// 会话只存在服务端内存中，Cookie 只携带不可猜测的随机标识（不携带用户编号），
// 因此"改请求参数冒充他人"无效：身份永远由会话解析得到。
// 进程重启会话失效，需重新登录；业务数据仍在数据库，不影响 AC-19。
package session

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"

	"onlineclass/internal/clock"
)

// Session 是一条会话记录。
type Session struct {
	UserID    string
	ExpiresAt time.Time
}

// Manager 是并发安全的会话表。
type Manager struct {
	mu    sync.Mutex
	items map[string]Session
	ttl   time.Duration
	clk   *clock.Clock
}

// NewManager 创建会话管理器；ttl 为会话有效期。
func NewManager(clk *clock.Clock, ttl time.Duration) *Manager {
	return &Manager{items: make(map[string]Session), ttl: ttl, clk: clk}
}

// Create 为用户新建会话并返回会话标识。
func (m *Manager) Create(userID string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	id := base64.RawURLEncoding.EncodeToString(raw)

	m.mu.Lock()
	defer m.mu.Unlock()
	m.items[id] = Session{UserID: userID, ExpiresAt: m.clk.Now().Add(m.ttl)}
	return id, nil
}

// Get 返回有效会话；过期会话被删除并视为不存在。
func (m *Manager) Get(id string) (Session, bool) {
	if id == "" {
		return Session{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.items[id]
	if !ok {
		return Session{}, false
	}
	if !s.ExpiresAt.After(m.clk.Now()) {
		delete(m.items, id)
		return Session{}, false
	}
	return s, true
}

// Destroy 删除会话（退出登录）。
func (m *Manager) Destroy(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.items, id)
}

// Sweep 清理已过期会话，返回清理数量。
func (m *Manager) Sweep() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.clk.Now()
	n := 0
	for id, s := range m.items {
		if !s.ExpiresAt.After(now) {
			delete(m.items, id)
			n++
		}
	}
	return n
}

// Len 返回当前会话数（测试与运维观测用）。
func (m *Manager) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.items)
}
