package store

import (
	"context"
	"fmt"
)

// CreateReminder 追加一条催交记录。
//
// 只提供 INSERT：题面要求催交记录"可多次追加、不可篡改"，
// 因此仓储层不提供 Update/Delete，改历史只能靠新增记录表达。
func (s *Store) CreateReminder(ctx context.Context, r Reminder) error {
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO reminders (id, assignment_id, student_id, assistant_id, channel, note, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.AssignmentID, r.StudentID, r.AssistantID, r.Channel, r.Note,
		formatTime(r.CreatedAt)); err != nil {
		return fmt.Errorf("写入催交记录失败: %w", err)
	}
	return nil
}

// ListReminders 返回某作业的催交记录，按时间正序（看板按学生分组展示）。
func (s *Store) ListReminders(ctx context.Context, assignmentID string) ([]Reminder, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, assignment_id, student_id, assistant_id, channel, note, created_at
		 FROM reminders WHERE assignment_id = ? ORDER BY created_at ASC, id ASC`, assignmentID)
	if err != nil {
		return nil, fmt.Errorf("查询催交记录失败: %w", err)
	}
	defer rows.Close()

	out := make([]Reminder, 0, 4)
	for rows.Next() {
		var (
			r       Reminder
			created string
		)
		if err := rows.Scan(&r.ID, &r.AssignmentID, &r.StudentID, &r.AssistantID, &r.Channel,
			&r.Note, &created); err != nil {
			return nil, fmt.Errorf("读取催交记录失败: %w", err)
		}
		r.CreatedAt = parseTime(created)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历催交记录失败: %w", err)
	}
	return out, nil
}
