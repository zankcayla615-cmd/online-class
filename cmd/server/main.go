// 命令 server 启动课下作业平台 HTTP 服务。
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"onlineclass/internal/clock"
	"onlineclass/internal/config"
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

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status": "ok",
			"now":    clk.Now().Format(time.RFC3339),
			"offset": clk.Offset().String(),
		})
	})

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("课下作业平台启动：http://localhost:%s 数据库=%s 教学时钟偏移=%s",
		cfg.Port, cfg.DBPath, clk.Offset())
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("服务退出: %v", err)
	}
}
