package session_test

import (
	"testing"
	"time"

	"onlineclass/internal/clock"
	"onlineclass/internal/session"
)

func TestCreateAndGet(t *testing.T) {
	m := session.NewManager(clock.New(0), time.Hour)
	id, err := m.Create("S01")
	if err != nil {
		t.Fatalf("创建会话失败: %v", err)
	}
	if len(id) < 32 {
		t.Fatalf("会话标识过短，易被猜测: %q", id)
	}
	got, ok := m.Get(id)
	if !ok {
		t.Fatal("会话应存在")
	}
	if got.UserID != "S01" {
		t.Fatalf("会话用户错误: %s", got.UserID)
	}
}

func TestGetUnknownAndEmpty(t *testing.T) {
	m := session.NewManager(clock.New(0), time.Hour)
	if _, ok := m.Get(""); ok {
		t.Fatal("空标识不应命中")
	}
	if _, ok := m.Get("not-exist"); ok {
		t.Fatal("不存在的标识不应命中")
	}
}

func TestExpiry(t *testing.T) {
	// ttl 为负：创建即过期，无需 sleep，结果确定。
	m := session.NewManager(clock.New(0), -time.Second)
	id, err := m.Create("S01")
	if err != nil {
		t.Fatalf("创建会话失败: %v", err)
	}
	if _, ok := m.Get(id); ok {
		t.Fatal("过期会话不应有效")
	}
	if m.Len() != 0 {
		t.Fatal("过期会话应被清除")
	}
}

func TestDestroy(t *testing.T) {
	m := session.NewManager(clock.New(0), time.Hour)
	id, _ := m.Create("T01")
	m.Destroy(id)
	if _, ok := m.Get(id); ok {
		t.Fatal("销毁后不应有效")
	}
}

func TestSweep(t *testing.T) {
	m := session.NewManager(clock.New(0), -time.Minute)
	if _, err := m.Create("S01"); err != nil {
		t.Fatalf("创建会话失败: %v", err)
	}
	if _, err := m.Create("S02"); err != nil {
		t.Fatalf("创建会话失败: %v", err)
	}
	if n := m.Sweep(); n != 2 {
		t.Fatalf("应清理 2 条过期会话，实际 %d", n)
	}
}

func TestIdentifiersAreUnique(t *testing.T) {
	m := session.NewManager(clock.New(0), time.Hour)
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		id, err := m.Create("S01")
		if err != nil {
			t.Fatalf("创建会话失败: %v", err)
		}
		if seen[id] {
			t.Fatal("会话标识重复")
		}
		seen[id] = true
	}
}
