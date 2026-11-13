package store

import (
	"errors"
	"time"
)

// ErrNotFound 表示查询对象不存在。调用方据此返回 404，避免泄露存在性。
var ErrNotFound = errors.New("记录不存在")

// ErrConflict 表示条件更新未命中（状态已被并发改变），调用方转 409。
var ErrConflict = errors.New("状态冲突")

// 角色常量。
const (
	RoleTeacher   = "teacher"
	RoleAssistant = "assistant"
	RoleStudent   = "student"
)

// 作业状态常量。
const (
	AssignmentDraft     = "DRAFT"
	AssignmentPublished = "PUBLISHED"
)

// 学生作业状态常量。
const (
	StateNotSubmitted = "NOT_SUBMITTED"
	StateSubmitted    = "SUBMITTED"
	StateReturned     = "RETURNED"
	StatePassed       = "PASSED"
)

// Enrollment 是"某学生在某作业上的状态行"，也是并发控制锚点。
type Enrollment struct {
	AssignmentID     string
	StudentID        string
	State            string
	StateVersion     int
	CurrentVersionNo int
}

// Submission 是一个提交版本（只追加，不更新）。
type Submission struct {
	ID           string
	AssignmentID string
	StudentID    string
	VersionNo    int
	Content      string
	ContentHash  string
	RequestID    string
	CreatedAt    time.Time
}

// Review 是一条批阅结论：一个提交版本最多一条（UNIQUE(version_id)）。
type Review struct {
	ID        string
	VersionID string
	Decision  string
	Feedback  string
	TeacherID string
	RequestID string
	CreatedAt time.Time
}

// 批阅结论常量。
const (
	ReviewPass   = "pass"
	ReviewReject = "reject"
)

// Extension 是补交授权：每个学生每份作业最多一条。
type Extension struct {
	AssignmentID string
	StudentID    string
	DeadlineAt   time.Time
	Reason       string
	GrantedBy    string
	GrantedAt    time.Time
}

// User 是账号。
type User struct {
	ID           string
	ClassID      string
	Name         string
	Role         string
	PasswordHash string
}

// Assignment 是作业。发布后以 Snapshot* 字段为准，Title/Requirement/DeadlineAt 不再变更。
type Assignment struct {
	ID        string
	ClassID   string
	TeacherID string
	State     string

	Title       string
	Requirement string
	DeadlineAt  time.Time

	SnapshotTitle       string
	SnapshotRequirement string
	SnapshotDeadlineAt  time.Time
	SnapshotBy          string
	SnapshotAt          time.Time

	PublishedAt time.Time
	CreatedAt   time.Time
}

// Published 表示作业已发布（存在快照）。
func (a Assignment) Published() bool { return a.State == AssignmentPublished }

// EffectiveTitle 返回学生应看到的标题：已发布取快照。
func (a Assignment) EffectiveTitle() string {
	if a.Published() && a.SnapshotTitle != "" {
		return a.SnapshotTitle
	}
	return a.Title
}

// EffectiveRequirement 返回学生应看到的提交要求：已发布取快照。
func (a Assignment) EffectiveRequirement() string {
	if a.Published() && a.SnapshotRequirement != "" {
		return a.SnapshotRequirement
	}
	return a.Requirement
}

// EffectiveDeadline 返回作业基准截止时间（未考虑个人补交）。
func (a Assignment) EffectiveDeadline() time.Time {
	if a.Published() && !a.SnapshotDeadlineAt.IsZero() {
		return a.SnapshotDeadlineAt
	}
	return a.DeadlineAt
}

const timeLayout = time.RFC3339Nano

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{timeLayout, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(timeLayout)
}
