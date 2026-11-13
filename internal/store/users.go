package store

import (
	"context"
	"database/sql"
	"fmt"
)

// GetUser 按编号查询账号；不存在返回 ErrNotFound。
func (s *Store) GetUser(ctx context.Context, id string) (User, error) {
	var u User
	err := s.db.QueryRowContext(ctx,
		`SELECT id, class_id, name, role, password_hash FROM users WHERE id = ?`, id).
		Scan(&u.ID, &u.ClassID, &u.Name, &u.Role, &u.PasswordHash)
	if err != nil {
		if err == sql.ErrNoRows {
			return User{}, ErrNotFound
		}
		return User{}, fmt.Errorf("查询账号失败: %w", err)
	}
	return u, nil
}

// ListStudents 返回指定班级的学生，按编号排序（汇总与看板使用）。
func (s *Store) ListStudents(ctx context.Context, classID string) ([]User, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, class_id, name, role, password_hash FROM users
		 WHERE class_id = ? AND role = 'student' ORDER BY id`, classID)
	if err != nil {
		return nil, fmt.Errorf("查询学生列表失败: %w", err)
	}
	defer rows.Close()

	var out []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.ClassID, &u.Name, &u.Role, &u.PasswordHash); err != nil {
			return nil, fmt.Errorf("读取学生失败: %w", err)
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历学生失败: %w", err)
	}
	return out, nil
}
