// 命令 server 启动课下作业平台 HTTP 服务。
package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"onlineclass/internal/clock"
	"onlineclass/internal/config"
	"onlineclass/internal/httpapi"
	"onlineclass/internal/store"
)

// 容器健康检查模式：`server -healthcheck http://127.0.0.1:8080/healthz`。
// distroless 镜像里没有 curl，用自身二进制做探针，避免为了探针换基础镜像。
func healthcheck(url string) int {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "健康检查失败: %v\n", err)
		return 1
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "健康检查失败: HTTP %d\n", resp.StatusCode)
		return 1
	}
	return 0
}

func main() {
	if len(os.Args) == 3 && os.Args[1] == "-healthcheck" {
		os.Exit(healthcheck(os.Args[2]))
	}

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
