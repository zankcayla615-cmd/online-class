package config_test

import (
	"testing"
	"time"

	"onlineclass/internal/config"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("PORT", "")
	t.Setenv("DB_PATH", "")
	t.Setenv("TEACHING_CLOCK_OFFSET", "")
	t.Setenv("SEED", "")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("加载默认配置失败: %v", err)
	}
	if cfg.Port != "8080" || cfg.DBPath != "data/app.db" || !cfg.Seed {
		t.Fatalf("默认值不符合预期: %+v", cfg)
	}
	if cfg.ClockOffset != 0 {
		t.Fatalf("默认时钟偏移应为 0，实际 %v", cfg.ClockOffset)
	}
}

func TestLoadClockOffset(t *testing.T) {
	t.Setenv("TEACHING_CLOCK_OFFSET", "2h30m")
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("加载失败: %v", err)
	}
	if cfg.ClockOffset != 2*time.Hour+30*time.Minute {
		t.Fatalf("偏移解析错误: %v", cfg.ClockOffset)
	}
}

func TestLoadInvalidOffset(t *testing.T) {
	t.Setenv("TEACHING_CLOCK_OFFSET", "两小时")
	if _, err := config.Load(); err == nil {
		t.Fatal("非法偏移应返回错误")
	}
}

func TestLoadEmptyPort(t *testing.T) {
	t.Setenv("PORT", "")
	t.Setenv("DB_PATH", "")
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("空 PORT 应回落到默认值: %v", err)
	}
	if cfg.Port == "" {
		t.Fatal("PORT 不应为空")
	}
}
