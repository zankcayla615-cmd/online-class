package store

import (
	"context"
	"database/sql"
	"fmt"
)

// GetSubmissionByVersionID 按版本编号读取提交，并用作业编号限定范围。
//
// 用 (assignment_id, id) 双条件而不是只按 id 查：避免跨作业的版本编号被拿来批阅。
func (s *Store) GetSubmissionByVersionID(ctx context.Context, assignmentID, versionID string) (Submission, error) {
	var (
		sub     Submission
		created string
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT id, assignment_id, student_id, version_no, content, content_hash, request_id, created_at
		 FROM submission_versions
		 WHERE id = ? AND assignment_id = ?`,
		versionID, assignmentID).
		Scan(&sub.ID, &sub.AssignmentID, &sub.StudentID, &sub.VersionNo, &sub.Content,
			&sub.ContentHash, &sub.RequestID, &created)
	if err != nil {
		if err == sql.ErrNoRows {
			return Submission{}, ErrNotFound
		}
		return Submission{}, fmt.Errorf("查询提交版本失败: %w", err)
	}
	sub.CreatedAt = parseTime(created)
	return sub, nil
}

// GetReviewByVersionID 读取某个版本的批阅结论；不存在返回 ErrNotFound。
func (s *Store) GetReviewByVersionID(ctx context.Context, versionID string) (Review, error) {
	var (
		rev     Review
		reqID   sql.NullString
		created string
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT id, version_id, decision, feedback, teacher_id, request_id, created_at
		 FROM reviews WHERE version_id = ?`, versionID).
		Scan(&rev.ID, &rev.VersionID, &rev.Decision, &rev.Feedback, &rev.TeacherID, &reqID, &created)
	if err != nil {
		if err == sql.ErrNoRows {
			return Review{}, ErrNotFound
		}
		return Review{}, fmt.Errorf("查询批阅结论失败: %w", err)
	}
	rev.RequestID = reqID.String
	rev.CreatedAt = parseTime(created)
	return rev, nil
}

// ListReviews 返回某学生某作业下的全部批阅结论（含已被新版本取代的旧结论）。
func (s *Store) ListReviews(ctx context.Context, assignmentID, studentID string) ([]Review, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT r.id, r.version_id, r.decision, r.feedback, r.teacher_id, r.request_id, r.created_at
		 FROM reviews r
		 JOIN submission_versions v ON v.id = r.version_id
		 WHERE v.assignment_id = ? AND v.student_id = ?
		 ORDER BY r.created_at ASC`, assignmentID, studentID)
	if err != nil {
		return nil, fmt.Errorf("查询批阅列表失败: %w", err)
	}
	defer rows.Close()

	out := make([]Review, 0, 2)
	for rows.Next() {
		var (
			rev     Review
			reqID   sql.NullString
			created string
		)
		if err := rows.Scan(&rev.ID, &rev.VersionID, &rev.Decision, &rev.Feedback,
			&rev.TeacherID, &reqID, &created); err != nil {
			return nil, fmt.Errorf("读取批阅结论失败: %w", err)
		}
		rev.RequestID = reqID.String
		rev.CreatedAt = parseTime(created)
		out = append(out, rev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历批阅结论失败: %w", err)
	}
	return out, nil
}

// CreateReviewParams 是一次批阅落库所需的全部信息。
type CreateReviewParams struct {
	Review            Review
	AssignmentID      string
	StudentID         string
	FromState         string
	ToState           string
	ExpectedVersionNo int
	ActorRole         string
	// Award 非空时在同一事务内发积分；唯一约束冲突视为"已发过"，不报错。
	Award *PointsEntry
}

// CreateReviewResult 回报本次批阅是否真的新发了积分。
type CreateReviewResult struct {
	Awarded bool
}

// CreateReview 在同一事务内：写批阅结论、推进状态行（乐观锁）、写审计事件。
//
// 只有"当前版本 + 状态为 SUBMITTED"才能成功；否则条件更新影响 0 行返回 ErrConflict。
// 同一版本第二个结论由 UNIQUE(version_id) 拦截。
func (s *Store) CreateReview(ctx context.Context, p CreateReviewParams) (CreateReviewResult, error) {
	var awarded bool
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		rev := p.Review
		var reqID any
		if rev.RequestID != "" {
			reqID = rev.RequestID
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO reviews (id, version_id, decision, feedback, teacher_id, request_id, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			rev.ID, rev.VersionID, rev.Decision, rev.Feedback, rev.TeacherID, reqID,
			formatTime(rev.CreatedAt)); err != nil {
			return fmt.Errorf("写入批阅结论失败: %w", err)
		}

		res, err := tx.ExecContext(ctx,
			`UPDATE enrollments
			 SET state = ?, state_version = state_version + 1
			 WHERE assignment_id = ? AND student_id = ? AND current_version_no = ? AND state = ?`,
			p.ToState, p.AssignmentID, p.StudentID, p.ExpectedVersionNo, p.FromState)
		if err != nil {
			return fmt.Errorf("推进批阅状态失败: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("读取状态更新结果失败: %w", err)
		}
		if n == 0 {
			return ErrConflict
		}

		label := "批阅通过"
		if rev.Decision == ReviewReject {
			label = "批阅退回"
		}
		detail := label
		if rev.Feedback != "" {
			detail = fmt.Sprintf("%s：%s", label, rev.Feedback)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO events
			 (assignment_id, student_id, actor_id, actor_role, type, detail, from_state, to_state, version_no, created_at)
			 VALUES (?, ?, ?, ?, 'reviewed', ?, ?, ?, ?, ?)`,
			p.AssignmentID, p.StudentID, rev.TeacherID, p.ActorRole, detail,
			p.FromState, p.ToState, p.ExpectedVersionNo, formatTime(rev.CreatedAt)); err != nil {
			return fmt.Errorf("写入批阅事件失败: %w", err)
		}

		if p.Award != nil {
			res, err := tx.ExecContext(ctx,
				`INSERT INTO points_ledger (id, student_id, assignment_id, reason, points, created_at)
				 VALUES (?, ?, ?, ?, ?, ?)
				 ON CONFLICT (assignment_id, student_id, reason) DO NOTHING`,
				p.Award.ID, p.Award.StudentID, p.Award.AssignmentID, p.Award.Reason,
				p.Award.Points, formatTime(p.Award.CreatedAt))
			if err != nil {
				return fmt.Errorf("写入积分流水失败: %w", err)
			}
			n, err := res.RowsAffected()
			if err != nil {
				return fmt.Errorf("读取发奖结果失败: %w", err)
			}
			// 影响 1 行 = 本次真的发奖；0 行 = 之前已发过，保持幂等。
			awarded = n == 1
		}
		return nil
	})
	return CreateReviewResult{Awarded: awarded}, err
}
