package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// CreateAssignment 插入一份作业（草稿）。
func (s *Store) CreateAssignment(ctx context.Context, a Assignment) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO assignments (id, class_id, teacher_id, title, requirement, deadline_at, state, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.ClassID, a.TeacherID, a.Title, a.Requirement, formatTime(a.DeadlineAt), a.State, formatTime(a.CreatedAt))
	if err != nil {
		return fmt.Errorf("插入作业失败: %w", err)
	}
	return nil
}

// UpdateAssignmentDraft 只更新草稿；已发布返回 ErrConflict（调用方转 409 assignment_locked）。
//
// 条件写在 WHERE 里，避免"先查后写"的竞态：并发发布与修改只会有一个成功。
func (s *Store) UpdateAssignmentDraft(ctx context.Context, id, title, requirement string, deadline time.Time) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE assignments SET title = ?, requirement = ?, deadline_at = ?
		 WHERE id = ? AND state = 'DRAFT'`,
		title, requirement, formatTime(deadline), id)
	if err != nil {
		return fmt.Errorf("更新草稿失败: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("读取更新结果失败: %w", err)
	}
	if n == 0 {
		var state string
		err := s.db.QueryRowContext(ctx, `SELECT state FROM assignments WHERE id = ?`, id).Scan(&state)
		if err == sql.ErrNoRows {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("查询作业状态失败: %w", err)
		}
		return ErrConflict
	}
	return nil
}

// PublishAssignment 在同一事务内完成：状态置为 PUBLISHED、把当前内容写入快照、为该班学生建状态行。
//
// 快照取自表的 title/requirement/deadline_at 列（而不是调用方传参），
// 保证快照与发布瞬间的草稿内容逐字一致；并发重复发布只有一次成功，其余返回 ErrConflict。
func (s *Store) PublishAssignment(ctx context.Context, id, by string, at time.Time) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE assignments
			 SET state = 'PUBLISHED',
			     published_at = ?,
			     snapshot_title = title,
			     snapshot_requirement = requirement,
			     snapshot_deadline_at = deadline_at,
			     snapshot_by = ?,
			     snapshot_at = ?
			 WHERE id = ? AND state = 'DRAFT'`,
			formatTime(at), by, formatTime(at), id)
		if err != nil {
			return fmt.Errorf("发布作业失败: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("读取发布结果失败: %w", err)
		}
		if n == 0 {
			var state string
			err := tx.QueryRowContext(ctx, `SELECT state FROM assignments WHERE id = ?`, id).Scan(&state)
			if err == sql.ErrNoRows {
				return ErrNotFound
			}
			if err != nil {
				return fmt.Errorf("查询作业状态失败: %w", err)
			}
			return ErrConflict
		}

		// 发布即固化班级花名册：此后新入班学生不追溯该作业，看板/汇总以状态行为准。
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO enrollments (assignment_id, student_id, state, state_version, current_version_no)
			 SELECT ?, id, 'NOT_SUBMITTED', 0, 0
			 FROM users
			 WHERE role = 'student' AND class_id = (SELECT class_id FROM assignments WHERE id = ?)`,
			id, id); err != nil {
			return fmt.Errorf("创建学生作业状态失败: %w", err)
		}
		return nil
	})
}

// CountEnrollments 返回某作业的状态行数量（测试与汇总使用）。
func (s *Store) CountEnrollments(ctx context.Context, assignmentID string) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM enrollments WHERE assignment_id = ?`, assignmentID).Scan(&n); err != nil {
		return 0, fmt.Errorf("统计状态行失败: %w", err)
	}
	return n, nil
}
