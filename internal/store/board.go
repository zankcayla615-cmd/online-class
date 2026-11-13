package store

import (
	"context"
	"database/sql"
	"fmt"
)

// ListEnrollmentRows 返回某作业全部学生的状态行（含姓名、版本数、首末提交时间）。
//
// 这里刻意不查 submission_versions.content：看板与汇总只需要状态与时间，
// 助教也不该通过统计接口摸到学生答案正文。
func (s *Store) ListEnrollmentRows(ctx context.Context, assignmentID string) ([]EnrollmentRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT e.student_id, u.name, e.state, e.current_version_no,
		        (SELECT COUNT(1) FROM submission_versions v
		          WHERE v.assignment_id = e.assignment_id AND v.student_id = e.student_id),
		        COALESCE((SELECT MIN(v.created_at) FROM submission_versions v
		          WHERE v.assignment_id = e.assignment_id AND v.student_id = e.student_id), ''),
		        COALESCE((SELECT MAX(v.created_at) FROM submission_versions v
		          WHERE v.assignment_id = e.assignment_id AND v.student_id = e.student_id), '')
		 FROM enrollments e
		 JOIN users u ON u.id = e.student_id
		 WHERE e.assignment_id = ?
		 ORDER BY e.student_id ASC`, assignmentID)
	if err != nil {
		return nil, fmt.Errorf("查询作业名单失败: %w", err)
	}
	defer rows.Close()

	out := make([]EnrollmentRow, 0, 8)
	for rows.Next() {
		var (
			row         EnrollmentRow
			first, last string
		)
		if err := rows.Scan(&row.StudentID, &row.StudentName, &row.State, &row.CurrentVersionNo,
			&row.SubmissionCount, &first, &last); err != nil {
			return nil, fmt.Errorf("读取作业名单失败: %w", err)
		}
		row.FirstSubmittedAt = parseTime(first)
		row.LastSubmittedAt = parseTime(last)
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历作业名单失败: %w", err)
	}
	return out, nil
}

// ListEvents 返回某作业的事件流；studentID 非空时只看该生。
func (s *Store) ListEvents(ctx context.Context, assignmentID, studentID string) ([]Event, error) {
	query := `SELECT id, COALESCE(assignment_id, ''), COALESCE(student_id, ''), actor_id, actor_role,
	                 type, detail, COALESCE(from_state, ''), COALESCE(to_state, ''), version_no, created_at
	          FROM events WHERE assignment_id = ?`
	args := []any{assignmentID}
	if studentID != "" {
		query += " AND student_id = ?"
		args = append(args, studentID)
	}
	query += " ORDER BY created_at ASC, id ASC"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询事件流失败: %w", err)
	}
	defer rows.Close()

	out := make([]Event, 0, 8)
	for rows.Next() {
		var (
			e         Event
			versionNo sql.NullInt64
			created   string
		)
		if err := rows.Scan(&e.ID, &e.AssignmentID, &e.StudentID, &e.ActorID, &e.ActorRole,
			&e.Type, &e.Detail, &e.FromState, &e.ToState, &versionNo, &created); err != nil {
			return nil, fmt.Errorf("读取事件失败: %w", err)
		}
		if versionNo.Valid {
			n := int(versionNo.Int64)
			e.VersionNo = &n
		}
		e.CreatedAt = parseTime(created)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历事件失败: %w", err)
	}
	return out, nil
}
