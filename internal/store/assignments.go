package store

import (
	"context"
	"database/sql"
	"fmt"
)

const assignmentColumns = `id, class_id, teacher_id, title, requirement, deadline_at, state,
	published_at, snapshot_title, snapshot_requirement, snapshot_deadline_at,
	snapshot_by, snapshot_at, created_at`

// GetAssignment 按编号查询作业；不存在返回 ErrNotFound。
// 班级校验由调用方（权限层）完成，避免仓储层掺入授权语义。
func (s *Store) GetAssignment(ctx context.Context, id string) (Assignment, error) {
	var (
		a                          Assignment
		publishedAt, snapshotBy    sql.NullString
		snapTitle, snapRequirement sql.NullString
		snapDeadline, snapshotAt   sql.NullString
		deadline, createdAt        string
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT `+assignmentColumns+` FROM assignments WHERE id = ?`, id).
		Scan(&a.ID, &a.ClassID, &a.TeacherID, &a.Title, &a.Requirement, &deadline, &a.State,
			&publishedAt, &snapTitle, &snapRequirement, &snapDeadline, &snapshotBy, &snapshotAt, &createdAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return Assignment{}, ErrNotFound
		}
		return Assignment{}, fmt.Errorf("查询作业失败: %w", err)
	}

	a.DeadlineAt = parseTime(deadline)
	a.CreatedAt = parseTime(createdAt)
	a.PublishedAt = parseTime(publishedAt.String)
	a.SnapshotTitle = snapTitle.String
	a.SnapshotRequirement = snapRequirement.String
	a.SnapshotDeadlineAt = parseTime(snapDeadline.String)
	a.SnapshotBy = snapshotBy.String
	a.SnapshotAt = parseTime(snapshotAt.String)
	return a, nil
}

// ListAssignmentsByClass 返回班级下的作业（教师工作台与学生首页使用）。
func (s *Store) ListAssignmentsByClass(ctx context.Context, classID string) ([]Assignment, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+assignmentColumns+` FROM assignments WHERE class_id = ? ORDER BY created_at DESC`, classID)
	if err != nil {
		return nil, fmt.Errorf("查询作业列表失败: %w", err)
	}
	defer rows.Close()

	var out []Assignment
	for rows.Next() {
		var (
			a                          Assignment
			publishedAt, snapshotBy    sql.NullString
			snapTitle, snapRequirement sql.NullString
			snapDeadline, snapshotAt   sql.NullString
			deadline, createdAt        string
		)
		if err := rows.Scan(&a.ID, &a.ClassID, &a.TeacherID, &a.Title, &a.Requirement, &deadline, &a.State,
			&publishedAt, &snapTitle, &snapRequirement, &snapDeadline, &snapshotBy, &snapshotAt, &createdAt); err != nil {
			return nil, fmt.Errorf("读取作业失败: %w", err)
		}
		a.DeadlineAt = parseTime(deadline)
		a.CreatedAt = parseTime(createdAt)
		a.PublishedAt = parseTime(publishedAt.String)
		a.SnapshotTitle = snapTitle.String
		a.SnapshotRequirement = snapRequirement.String
		a.SnapshotDeadlineAt = parseTime(snapDeadline.String)
		a.SnapshotBy = snapshotBy.String
		a.SnapshotAt = parseTime(snapshotAt.String)
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历作业失败: %w", err)
	}
	return out, nil
}
