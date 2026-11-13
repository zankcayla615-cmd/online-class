// 命令 server 启动课下作业平台 HTTP 服务。
package main

import (
	"log"
	"net/http"
	"time"

	"onlineclass/internal/clock"
	"onlineclass/internal/config"
	"onlineclass/internal/httpapi"
	"onlineclass/internal/store"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("配置错误: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("打开数据库失败: %v", err)
	}
	defer func() { _ = st.Close() }()

	if err := st.Migrate(); err != nil {
		log.Fatalf("数据库迁移失败: %v", err)
	}
	if cfg.Seed {
		if err := st.Seed(); err != nil {
			log.Fatalf("写入演示数据失败: %v", err)
		}
	}

	clk := clock.New(cfg.ClockOffset)
	app := httpapi.New(st, clk, 8*time.Hour)

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           app.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("课下作业平台启动：http://localhost:%s 数据库=%s 教学时钟偏移=%s",
		cfg.Port, cfg.DBPath, clk.Offset())
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("服务退出: %v", err)
	}
}
