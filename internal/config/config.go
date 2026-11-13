// Package config 读取运行配置。所有配置只来自环境变量，默认值面向教学演示。
package config

import (
	"fmt"
	"os"
	"time"
)

// Config 是服务运行所需的全部配置。
type Config struct {
	// Port 监听端口，默认 8080。
	Port string
	// DBPath SQLite 数据库文件路径，默认 data/app.db。
	DBPath string
	// ClockOffset 教学时钟偏移，用于演示逾期与补交；默认 0。
	ClockOffset time.Duration
	// Seed 是否在启动时写入预置演示数据，默认 true。
	Seed bool
}

// Load 从环境变量加载配置并校验。
//
// 环境变量：
//
//	PORT                   监听端口
//	DB_PATH                数据库文件
//	TEACHING_CLOCK_OFFSET  教学时钟偏移（如 2h、-30m、90s）
//	SEED                   1/0，是否写入演示数据
func Load() (Config, error) {
	cfg := Config{
		Port:   env("PORT", "8080"),
		DBPath: env("DB_PATH", "data/app.db"),
		Seed:   env("SEED", "1") == "1",
	}
	if cfg.Port == "" {
		return cfg, fmt.Errorf("PORT 不能为空")
	}
	if cfg.DBPath == "" {
		return cfg, fmt.Errorf("DB_PATH 不能为空")
	}
	raw := env("TEACHING_CLOCK_OFFSET", "")
	if raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return cfg, fmt.Errorf("TEACHING_CLOCK_OFFSET 格式错误（示例 2h、-30m）: %w", err)
		}
		cfg.ClockOffset = d
	}
	return cfg, nil
}

// env 读取环境变量；未设置或设为空串时回落默认值（空串等同未设置，便于
// 容器与 CI 中以空值占位而不破坏默认配置）。
func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}
