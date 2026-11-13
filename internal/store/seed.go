package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"onlineclass/internal/auth"
)

// DemoPassword 预置账号口令。仅用于教学演示：页面与技术文档都会标注，
// 演示视频中需脱敏；生产环境必须改为首次登录强制改密。
const DemoPassword = "demo1234"

type seedClass struct{ ID, Name string }

type seedUser struct{ ID, Name, Role, ClassID string }

var seedClasses = []seedClass{
	{ID: "P01", Name: "Go 后端春季班"},
	{ID: "P02", Name: "外班（对照用）"},
}

var seedUsers = []seedUser{
	{ID: "T01", Name: "周老师", Role: "teacher", ClassID: "P01"},
	{ID: "A01", Name: "李助教", Role: "assistant", ClassID: "P01"},
	{ID: "S01", Name: "林小雨", Role: "student", ClassID: "P01"},
	{ID: "S02", Name: "王浩然", Role: "student", ClassID: "P01"},
	{ID: "S03", Name: "陈子墨", Role: "student", ClassID: "P01"},
	{ID: "S04", Name: "赵嘉宁", Role: "student", ClassID: "P01"},
	{ID: "S05", Name: "吴一诺", Role: "student", ClassID: "P01"},
	{ID: "X01", Name: "外班同学", Role: "student", ClassID: "P02"},
}

// Seed 写入预置班级与账号，可重复执行（已存在则跳过）。
// 时间使用系统时间：预置数据不属于业务时序，不参与截止与发奖判定。
func (s *Store) Seed() error {
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339)

	return s.Tx(ctx, func(tx *sql.Tx) error {
		for _, c := range seedClasses {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO classes (id, name) VALUES (?, ?) ON CONFLICT (id) DO NOTHING`,
				c.ID, c.Name); err != nil {
				return fmt.Errorf("写入班级 %s 失败: %w", c.ID, err)
			}
		}

		existing := map[string]bool{}
		rows, err := tx.QueryContext(ctx, `SELECT id FROM users`)
		if err != nil {
			return fmt.Errorf("查询已有账号失败: %w", err)
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return fmt.Errorf("读取账号失败: %w", err)
			}
			existing[id] = true
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return fmt.Errorf("遍历账号失败: %w", err)
		}
		rows.Close()

		for _, u := range seedUsers {
			if existing[u.ID] {
				continue
			}
			hash, err := auth.HashPassword(DemoPassword)
			if err != nil {
				return fmt.Errorf("生成 %s 口令失败: %w", u.ID, err)
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO users (id, class_id, name, role, password_hash, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
				u.ID, u.ClassID, u.Name, u.Role, hash, now); err != nil {
				return fmt.Errorf("写入账号 %s 失败: %w", u.ID, err)
			}
		}
		return nil
	})
}
