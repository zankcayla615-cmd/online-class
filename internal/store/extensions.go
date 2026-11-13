package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// CreateExtensionParams 是一次补交授权落库所需的全部信息。
type CreateExtensionParams struct {
	Extension Extension
	// State 是该生当前状态，写事件时作为 before/after（补交不改状态，只改有效截止）。
	State     string
	ActorRole string
}

// CreateExtension 写入补交授权并记一条事件；同一学生同一作业的第二次写入由主键拦截。
func (s *Store) CreateExtension(ctx context.Context, p CreateExtensionParams) error {
	ext := p.Extension
	return s.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO extensions (assignment_id, student_id, deadline_at, reason, granted_by, granted_at)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			ext.AssignmentID, ext.StudentID, formatTime(ext.DeadlineAt), ext.Reason,
			ext.GrantedBy, formatTime(ext.GrantedAt)); err != nil {
			return fmt.Errorf("写入补交授权失败: %w", err)
		}

		detail := fmt.Sprintf("批准补交：有效截止 %s，原因：%s", ext.DeadlineAt.Format(time.RFC3339), ext.Reason)
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO events
			 (assignment_id, student_id, actor_id, actor_role, type, detail, from_state, to_state, version_no, created_at)
			 VALUES (?, ?, ?, ?, 'extension_granted', ?, ?, ?, NULL, ?)`,
			ext.AssignmentID, ext.StudentID, ext.GrantedBy, p.ActorRole, detail,
			p.State, p.State, formatTime(ext.GrantedAt)); err != nil {
			return fmt.Errorf("写入补交事件失败: %w", err)
		}
		return nil
	})
}

// ListExtensions 返回某作业的全部补交授权（汇总与看板用）。
func (s *Store) ListExtensions(ctx context.Context, assignmentID string) ([]Extension, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT assignment_id, student_id, deadline_at, reason, granted_by, granted_at
		 FROM extensions WHERE assignment_id = ? ORDER BY granted_at ASC`, assignmentID)
	if err != nil {
		return nil, fmt.Errorf("查询补交授权失败: %w", err)
	}
	defer rows.Close()

	out := make([]Extension, 0, 2)
	for rows.Next() {
		var (
			ext             Extension
			deadline, grant string
		)
		if err := rows.Scan(&ext.AssignmentID, &ext.StudentID, &deadline, &ext.Reason,
			&ext.GrantedBy, &grant); err != nil {
			return nil, fmt.Errorf("读取补交授权失败: %w", err)
		}
		ext.DeadlineAt = parseTime(deadline)
		ext.GrantedAt = parseTime(grant)
		out = append(out, ext)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历补交授权失败: %w", err)
	}
	return out, nil
}
