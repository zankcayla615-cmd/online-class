package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// 积分发放口径：只有"首次通过"发奖，固定 10 分；本题不实现撤销或扣减。
const (
	ReasonFirstPass = "first_pass"
	PointsFirstPass = 10
)

// PointsEntry 是一条积分流水。
type PointsEntry struct {
	ID           string
	StudentID    string
	AssignmentID string
	Reason       string
	Points       int
	CreatedAt    time.Time
}

// GetPointsEntry 读取单条流水；不存在返回 ErrNotFound。
func (s *Store) GetPointsEntry(ctx context.Context, assignmentID, studentID, reason string) (PointsEntry, error) {
	var (
		e       PointsEntry
		created string
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT id, student_id, assignment_id, reason, points, created_at
		 FROM points_ledger WHERE assignment_id = ? AND student_id = ? AND reason = ?`,
		assignmentID, studentID, reason).
		Scan(&e.ID, &e.StudentID, &e.AssignmentID, &e.Reason, &e.Points, &created)
	if err != nil {
		if err == sql.ErrNoRows {
			return PointsEntry{}, ErrNotFound
		}
		return PointsEntry{}, fmt.Errorf("查询积分流水失败: %w", err)
	}
	e.CreatedAt = parseTime(created)
	return e, nil
}

// ListPointsByAssignment 返回某作业的全部奖励流水（教师汇总与时间线用）。
func (s *Store) ListPointsByAssignment(ctx context.Context, assignmentID string) ([]PointsEntry, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, student_id, assignment_id, reason, points, created_at
		 FROM points_ledger WHERE assignment_id = ? ORDER BY created_at ASC`, assignmentID)
	if err != nil {
		return nil, fmt.Errorf("查询作业积分流水失败: %w", err)
	}
	defer rows.Close()

	out := make([]PointsEntry, 0, 4)
	for rows.Next() {
		var (
			e       PointsEntry
			created string
		)
		if err := rows.Scan(&e.ID, &e.StudentID, &e.AssignmentID, &e.Reason, &e.Points, &created); err != nil {
			return nil, fmt.Errorf("读取积分流水失败: %w", err)
		}
		e.CreatedAt = parseTime(created)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历积分流水失败: %w", err)
	}
	return out, nil
}

// TotalPoints 返回某学生的累计积分（学生页展示用）。
func (s *Store) TotalPoints(ctx context.Context, studentID string) (int, error) {
	var total sql.NullInt64
	if err := s.db.QueryRowContext(ctx,
		`SELECT SUM(points) FROM points_ledger WHERE student_id = ?`, studentID).Scan(&total); err != nil {
		return 0, fmt.Errorf("统计学生积分失败: %w", err)
	}
	return int(total.Int64), nil
}
