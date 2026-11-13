package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// GetEnrollment 读取学生作业状态行；不存在返回 ErrNotFound。
func (s *Store) GetEnrollment(ctx context.Context, assignmentID, studentID string) (Enrollment, error) {
	var e Enrollment
	err := s.db.QueryRowContext(ctx,
		`SELECT assignment_id, student_id, state, state_version, current_version_no
		 FROM enrollments WHERE assignment_id = ? AND student_id = ?`,
		assignmentID, studentID).
		Scan(&e.AssignmentID, &e.StudentID, &e.State, &e.StateVersion, &e.CurrentVersionNo)
	if err != nil {
		if err == sql.ErrNoRows {
			return Enrollment{}, ErrNotFound
		}
		return Enrollment{}, fmt.Errorf("查询学生作业状态失败: %w", err)
	}
	return e, nil
}

// GetExtension 读取补交授权；不存在返回 ErrNotFound。
func (s *Store) GetExtension(ctx context.Context, assignmentID, studentID string) (Extension, error) {
	var (
		ext             Extension
		deadline, grant string
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT assignment_id, student_id, deadline_at, reason, granted_by, granted_at
		 FROM extensions WHERE assignment_id = ? AND student_id = ?`,
		assignmentID, studentID).
		Scan(&ext.AssignmentID, &ext.StudentID, &deadline, &ext.Reason, &ext.GrantedBy, &grant)
	if err != nil {
		if err == sql.ErrNoRows {
			return Extension{}, ErrNotFound
		}
		return Extension{}, fmt.Errorf("查询补交授权失败: %w", err)
	}
	ext.DeadlineAt = parseTime(deadline)
	ext.GrantedAt = parseTime(grant)
	return ext, nil
}

// GetSubmissionByRequestID 按请求号查询已存在的版本，用于识别重复请求。
func (s *Store) GetSubmissionByRequestID(ctx context.Context, assignmentID, studentID, requestID string) (Submission, error) {
	var (
		sub     Submission
		created string
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT id, assignment_id, student_id, version_no, content, content_hash, request_id, created_at
		 FROM submission_versions
		 WHERE assignment_id = ? AND student_id = ? AND request_id = ?`,
		assignmentID, studentID, requestID).
		Scan(&sub.ID, &sub.AssignmentID, &sub.StudentID, &sub.VersionNo, &sub.Content,
			&sub.ContentHash, &sub.RequestID, &created)
	if err != nil {
		if err == sql.ErrNoRows {
			return Submission{}, ErrNotFound
		}
		return Submission{}, fmt.Errorf("查询提交请求失败: %w", err)
	}
	sub.CreatedAt = parseTime(created)
	return sub, nil
}

// ListSubmissions 返回某学生某作业的全部版本，按版本号升序（含历史版本）。
func (s *Store) ListSubmissions(ctx context.Context, assignmentID, studentID string) ([]Submission, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, assignment_id, student_id, version_no, content, content_hash, request_id, created_at
		 FROM submission_versions
		 WHERE assignment_id = ? AND student_id = ?
		 ORDER BY version_no ASC`,
		assignmentID, studentID)
	if err != nil {
		return nil, fmt.Errorf("查询提交版本失败: %w", err)
	}
	defer rows.Close()

	out := make([]Submission, 0, 2)
	for rows.Next() {
		var (
			sub     Submission
			created string
		)
		if err := rows.Scan(&sub.ID, &sub.AssignmentID, &sub.StudentID, &sub.VersionNo, &sub.Content,
			&sub.ContentHash, &sub.RequestID, &created); err != nil {
			return nil, fmt.Errorf("读取提交版本失败: %w", err)
		}
		sub.CreatedAt = parseTime(created)
		out = append(out, sub)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历提交版本失败: %w", err)
	}
	return out, nil
}

// SubmitVersionParams 是一次提交落库所需的全部信息。
type SubmitVersionParams struct {
	Submission        Submission
	FromState         string
	ToState           string
	ExpectedVersionNo int
	ActorRole         string
}

// SubmitVersion 在同一事务内：追加版本、推进状态行（乐观锁）、写审计事件。
//
// 并发保护有两层：
//  1. UNIQUE(assignment, student, version_no) 与 UNIQUE(..., request_id) 兜底重复插入；
//  2. UPDATE ... WHERE current_version_no = 期望值 保证状态行只被推进一次。
//
// 任一条件不满足返回 ErrConflict，由业务层转 409，绝不静默覆盖。
func (s *Store) SubmitVersion(ctx context.Context, p SubmitVersionParams) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		sub := p.Submission
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO submission_versions
			 (id, assignment_id, student_id, version_no, content, content_hash, request_id, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			sub.ID, sub.AssignmentID, sub.StudentID, sub.VersionNo, sub.Content,
			sub.ContentHash, sub.RequestID, formatTime(sub.CreatedAt)); err != nil {
			return fmt.Errorf("写入提交版本失败: %w", err)
		}

		res, err := tx.ExecContext(ctx,
			`UPDATE enrollments
			 SET state = ?, current_version_no = ?, state_version = state_version + 1
			 WHERE assignment_id = ? AND student_id = ? AND current_version_no = ? AND state = ?`,
			p.ToState, sub.VersionNo, sub.AssignmentID, sub.StudentID, p.ExpectedVersionNo, p.FromState)
		if err != nil {
			return fmt.Errorf("推进作业状态失败: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("读取状态更新结果失败: %w", err)
		}
		if n == 0 {
			return ErrConflict
		}

		if _, err := tx.ExecContext(ctx,
			`INSERT INTO events
			 (assignment_id, student_id, actor_id, actor_role, type, detail, from_state, to_state, version_no, created_at)
			 VALUES (?, ?, ?, ?, 'submitted', ?, ?, ?, ?, ?)`,
			sub.AssignmentID, sub.StudentID, sub.StudentID, p.ActorRole,
			fmt.Sprintf("提交第 %d 版", sub.VersionNo), p.FromState, p.ToState, sub.VersionNo,
			formatTime(sub.CreatedAt)); err != nil {
			return fmt.Errorf("写入审计事件失败: %w", err)
		}
		return nil
	})
}

// IsUniqueViolation 判断错误是否由唯一约束冲突导致。
//
// 业务层用它把"并发下同版本重复插入"翻译成 409，而不是 500。
func IsUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	var se interface{ Code() int }
	if errors.As(err, &se) {
		// modernc/sqlite：2067 = SQLITE_CONSTRAINT_UNIQUE，1555 = 主键冲突
		if se.Code() == 2067 || se.Code() == 1555 {
			return true
		}
	}
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}
