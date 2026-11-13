package store_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"onlineclass/internal/auth"
	"onlineclass/internal/store"
)

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestMigrateIsIdempotent(t *testing.T) {
	st := openTestStore(t)
	for i := 0; i < 3; i++ {
		if err := st.Migrate(); err != nil {
			t.Fatalf("第 %d 次迁移失败: %v", i+1, err)
		}
	}
	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(1) FROM schema_migrations`).Scan(&n); err != nil {
		t.Fatalf("查询迁移记录失败: %v", err)
	}
	if n != 1 {
		t.Fatalf("应只有 1 条迁移记录，实际 %d", n)
	}
}

func TestSeedIsIdempotentAndSeedsExpectedData(t *testing.T) {
	st := openTestStore(t)
	if err := st.Migrate(); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	if err := st.Seed(); err != nil {
		t.Fatalf("首次写入种子失败: %v", err)
	}
	if err := st.Seed(); err != nil {
		t.Fatalf("重复写入种子应成功: %v", err)
	}

	counts := map[string]int{}
	for _, q := range []string{
		`SELECT COUNT(1) FROM users`,
		`SELECT COUNT(1) FROM classes`,
		`SELECT COUNT(1) FROM users WHERE role = 'student' AND class_id = 'P01'`,
	} {
		var n int
		if err := st.DB().QueryRow(q).Scan(&n); err != nil {
			t.Fatalf("统计失败 %s: %v", q, err)
		}
		counts[q] = n
	}
	if counts[`SELECT COUNT(1) FROM users`] != 8 {
		t.Fatalf("应有 8 个账号，实际 %d", counts[`SELECT COUNT(1) FROM users`])
	}
	if counts[`SELECT COUNT(1) FROM classes`] != 2 {
		t.Fatalf("应有 2 个班级，实际 %d", counts[`SELECT COUNT(1) FROM classes`])
	}
	if counts[`SELECT COUNT(1) FROM users WHERE role = 'student' AND class_id = 'P01'`] != 5 {
		t.Fatalf("P01 应有 5 名学生，实际 %d", counts[`SELECT COUNT(1) FROM users WHERE role = 'student' AND class_id = 'P01'`])
	}

	var hash string
	if err := st.DB().QueryRow(`SELECT password_hash FROM users WHERE id = 'T01'`).Scan(&hash); err != nil {
		t.Fatalf("读取口令哈希失败: %v", err)
	}
	if !auth.VerifyPassword(hash, store.DemoPassword) {
		t.Fatal("预置账号口令校验失败")
	}
}

func TestUniqueRequestIDPerStudentAssignment(t *testing.T) {
	st := openTestStore(t)
	mustMigrateSeed(t, st)
	insertEnrollment(t, st)

	insert := func(id, reqID string, no int) error {
		_, err := st.DB().Exec(`INSERT INTO submission_versions
			(id, assignment_id, student_id, version_no, content, content_hash, request_id, created_at)
			VALUES (?, 'HW1', 'S01', ?, '内容', 'h', ?, '2026-11-13T00:00:00Z')`, id, no, reqID)
		return err
	}
	if err := insert("v-1", "req-a", 1); err != nil {
		t.Fatalf("首次插入失败: %v", err)
	}
	if err := insert("v-2", "req-a", 2); err == nil {
		t.Fatal("相同 request_id 应被唯一约束拒绝（不得产生额外版本）")
	}
	if err := insert("v-3", "req-b", 2); err != nil {
		t.Fatalf("不同 request_id 应可插入: %v", err)
	}
}

func TestUniquePointsLedger(t *testing.T) {
	st := openTestStore(t)
	mustMigrateSeed(t, st)
	insertEnrollment(t, st) // 积分流水外键依赖 assignments

	exec := func() error {
		_, err := st.DB().Exec(`INSERT INTO points_ledger
			(id, student_id, assignment_id, reason, points, created_at)
			VALUES (?, 'S01', 'HW1', 'first_pass', 10, '2026-11-13T00:00:00Z')`, "l-"+randSuffix())
		return err
	}
	if err := exec(); err != nil {
		t.Fatalf("首次发奖失败: %v", err)
	}
	if err := exec(); err == nil {
		t.Fatal("同一学生同一作业同一原因应被唯一约束拒绝（不得重复发奖）")
	}
}

func TestTxRollbackOnError(t *testing.T) {
	st := openTestStore(t)
	mustMigrateSeed(t, st)

	sentinel := errors.New("boom")
	err := st.Tx(context.Background(), func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO classes (id, name) VALUES ('P99', '临时班')`); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("应返回原始错误，实际 %v", err)
	}
	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(1) FROM classes WHERE id = 'P99'`).Scan(&n); err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if n != 0 {
		t.Fatal("事务回滚后不应留下数据")
	}
}

func mustMigrateSeed(t *testing.T, st *store.Store) {
	t.Helper()
	if err := st.Migrate(); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	if err := st.Seed(); err != nil {
		t.Fatalf("种子失败: %v", err)
	}
}

// insertEnrollment 建立 S01 与作业 HW1 的状态行（版本表依赖该行）。
func insertEnrollment(t *testing.T, st *store.Store) {
	t.Helper()
	if _, err := st.DB().Exec(`INSERT INTO assignments
		(id, class_id, teacher_id, title, requirement, deadline_at, state, created_at)
		VALUES ('HW1', 'P01', 'T01', '测试作业', '要求', '2026-12-01T00:00:00Z', 'DRAFT', '2026-11-13T00:00:00Z')`); err != nil {
		t.Fatalf("插入作业失败: %v", err)
	}
	if _, err := st.DB().Exec(`INSERT INTO enrollments
		(assignment_id, student_id, state, state_version, current_version_no)
		VALUES ('HW1', 'S01', 'SUBMITTED', 1, 0)`); err != nil {
		t.Fatalf("插入学生作业状态失败: %v", err)
	}
}

var randSeq int

func randSuffix() string {
	randSeq++
	return string(rune('a' + randSeq%26))
}
