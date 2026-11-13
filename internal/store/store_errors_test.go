package store_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"onlineclass/internal/store"
)

// TestClosedStoreReturnsErrors 关闭连接后每个仓储入口都应返回错误而不是静默成功。
// 这些分支在正常流程里看不到，但决定了"数据库不可用时会不会假装写入成功"。
func TestClosedStoreReturnsErrors(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "closed.db"))
	if err != nil {
		t.Fatalf("打开库失败: %v", err)
	}
	if err := st.Migrate(); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	if err := st.Seed(); err != nil {
		t.Fatalf("种子失败: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("关闭失败: %v", err)
	}

	ctx := context.Background()
	now := time.Now().UTC()
	sub := store.Submission{
		ID: "SUB-X", AssignmentID: "HW1", StudentID: "S01", VersionNo: 1,
		Content: "x", ContentHash: "h", RequestID: "r", CreatedAt: now,
	}
	cases := []struct {
		name string
		call func() error
	}{
		{"GetAssignment", func() error { _, err := st.GetAssignment(ctx, "HW1"); return err }},
		{"ListAssignmentsByClass", func() error { _, err := st.ListAssignmentsByClass(ctx, "P01"); return err }},
		{"CreateAssignment", func() error {
			return st.CreateAssignment(ctx, store.Assignment{ID: "HW-X", ClassID: "P01", TeacherID: "T01",
				Title: "t", Requirement: "r", DeadlineAt: now, State: store.AssignmentDraft, CreatedAt: now})
		}},
		{"UpdateAssignmentDraft", func() error { return st.UpdateAssignmentDraft(ctx, "HW1", "t", "r", now) }},
		{"PublishAssignment", func() error { return st.PublishAssignment(ctx, "HW1", "T01", now) }},
		{"CountEnrollments", func() error { _, err := st.CountEnrollments(ctx, "HW1"); return err }},
		{"GetUser", func() error { _, err := st.GetUser(ctx, "S01"); return err }},
		{"GetEnrollment", func() error { _, err := st.GetEnrollment(ctx, "HW1", "S01"); return err }},
		{"SubmitVersion", func() error {
			return st.SubmitVersion(ctx, store.SubmitVersionParams{
				Submission: sub, FromState: store.StateNotSubmitted, ToState: store.StateSubmitted,
				ExpectedVersionNo: 0, ActorRole: store.RoleStudent,
			})
		}},
		{"ListSubmissions", func() error { _, err := st.ListSubmissions(ctx, "HW1", "S01"); return err }},
		{"ListReviews", func() error { _, err := st.ListReviews(ctx, "HW1", "S01"); return err }},
		{"CreateReview", func() error {
			_, err := st.CreateReview(ctx, store.CreateReviewParams{
				Review: store.Review{ID: "RV-X", VersionID: "SUB-X", Decision: store.ReviewPass,
					TeacherID: "T01", CreatedAt: now},
				AssignmentID: "HW1", StudentID: "S01", FromState: store.StateSubmitted,
				ToState: store.StatePassed, ExpectedVersionNo: 1, ActorRole: store.RoleTeacher,
			})
			return err
		}},
		{"CreateReminder", func() error {
			return st.CreateReminder(ctx, store.Reminder{ID: "RM-X", AssignmentID: "HW1",
				StudentID: "S01", AssistantID: "A01", Channel: store.ChannelPhone, Note: "x", CreatedAt: now})
		}},
		{"ListReminders", func() error { _, err := st.ListReminders(ctx, "HW1"); return err }},
		{"ListExtensions", func() error { _, err := st.ListExtensions(ctx, "HW1"); return err }},
		{"GetExtension", func() error { _, err := st.GetExtension(ctx, "HW1", "S01"); return err }},
		{"ListPointsByAssignment", func() error { _, err := st.ListPointsByAssignment(ctx, "HW1"); return err }},
		{"TotalPoints", func() error { _, err := st.TotalPoints(ctx, "S01"); return err }},
		{"ListEnrollmentRows", func() error { _, err := st.ListEnrollmentRows(ctx, "HW1"); return err }},
		{"ListEvents", func() error { _, err := st.ListEvents(ctx, "HW1", ""); return err }},
	}
	for _, c := range cases {
		if err := c.call(); err == nil {
			t.Fatalf("%s 在连接关闭后应返回错误", c.name)
		}
	}
}

// TestOpenFailsWhenParentIsFile 目录位置被文件占用时应返回可读错误。
func TestOpenFailsWhenParentIsFile(t *testing.T) {
	block := filepath.Join(t.TempDir(), "block")
	if err := os.WriteFile(block, []byte("x"), 0o600); err != nil {
		t.Fatalf("准备占位文件失败: %v", err)
	}
	if _, err := store.Open(filepath.Join(block, "app.db")); err == nil {
		t.Fatal("父路径是文件时应打开失败")
	}
}

// TestCloseNilStore 未打开的 Store 关闭应安全返回。
func TestCloseNilStore(t *testing.T) {
	var st store.Store
	if err := st.Close(); err != nil {
		t.Fatalf("空 Store 关闭应返回 nil，实际 %v", err)
	}
}

// TestSeedAndMigrateAreRepeatable 重复执行不应报错（启动幂等）。
func TestSeedAndMigrateAreRepeatable(t *testing.T) {
	st := openTestStore(t)
	if err := st.Migrate(); err != nil {
		t.Fatalf("第二次迁移应跳过，实际 %v", err)
	}
	if err := st.Seed(); err != nil {
		t.Fatalf("第二次种子应跳过，实际 %v", err)
	}
	if err := st.Seed(); err != nil {
		t.Fatalf("第三次种子应跳过，实际 %v", err)
	}
}

// TestIsUniqueViolation 只把真正的唯一约束错误当成"已存在"。
func TestIsUniqueViolation(t *testing.T) {
	if store.IsUniqueViolation(nil) {
		t.Fatal("nil 不应判为唯一约束冲突")
	}
	if store.IsUniqueViolation(errors.New("disk I/O error")) {
		t.Fatal("普通错误不应判为唯一约束冲突")
	}
}

// TestAssignmentEffectiveFallbacks 草稿用原始字段，已发布优先用快照，缺失时回退。
func TestAssignmentEffectiveFallbacks(t *testing.T) {
	deadline := time.Date(2026, 12, 1, 10, 0, 0, 0, time.UTC)
	draft := store.Assignment{
		State: store.AssignmentDraft, Title: "草稿标题", Requirement: "草稿要求", DeadlineAt: deadline,
		SnapshotTitle: "快照标题", SnapshotRequirement: "快照要求", SnapshotDeadlineAt: deadline.Add(time.Hour),
	}
	if draft.EffectiveTitle() != "草稿标题" || draft.EffectiveRequirement() != "草稿要求" {
		t.Fatal("草稿应使用原始字段")
	}
	if !draft.EffectiveDeadline().Equal(deadline) {
		t.Fatal("草稿应使用原始截止")
	}

	published := draft
	published.State = store.AssignmentPublished
	if published.EffectiveTitle() != "快照标题" || published.EffectiveRequirement() != "快照要求" {
		t.Fatal("已发布应使用快照")
	}
	if !published.EffectiveDeadline().Equal(deadline.Add(time.Hour)) {
		t.Fatal("已发布应使用快照截止")
	}

	// 快照字段缺失（历史数据/异常迁移）时回退到原始字段，避免页面空白
	empty := store.Assignment{State: store.AssignmentPublished, Title: "标题", Requirement: "要求", DeadlineAt: deadline}
	if empty.EffectiveTitle() != "标题" || empty.EffectiveRequirement() != "要求" {
		t.Fatal("快照缺失应回退原始字段")
	}
	if !empty.EffectiveDeadline().Equal(deadline) {
		t.Fatal("快照截止缺失应回退原始截止")
	}
}
