package service_test

import (
	"context"
	"sync"
	"testing"

	"onlineclass/internal/service"
	"onlineclass/internal/store"
)

// TestSubmitStaleBaseVersionRejected 验证旧页面/旧请求不能覆盖新提交：
// base_version 落后或超前都返回 409，且服务端内容完全不变。
func TestSubmitStaleBaseVersionRejected(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	a := e.createPublished(t)
	svc := service.NewSubmissionService(e.st, e.clk)
	s01 := e.user(t, "S01")

	if _, err := svc.Submit(ctx, s01, a.ID, submitAt("第一版", "req-1", 0)); err != nil {
		t.Fatalf("首次提交失败: %v", err)
	}

	// 页面停留在 v0（落后一版）→ 拒绝
	_, err := svc.Submit(ctx, s01, a.ID, submitAt("旧页面覆盖", "req-stale", 0))
	if err == nil || code(t, err) != "stale_version" {
		t.Fatalf("base_version 落后应返回 stale_version，实际 %v", err)
	}

	// 客户端自称 v5（超前）→ 同样拒绝，不猜测意图
	_, err = svc.Submit(ctx, s01, a.ID, submitAt("未来版本覆盖", "req-future", 5))
	if err == nil || code(t, err) != "stale_version" {
		t.Fatalf("base_version 超前应返回 stale_version，实际 %v", err)
	}

	versions, err := e.st.ListSubmissions(ctx, a.ID, "S01")
	if err != nil {
		t.Fatalf("查询版本失败: %v", err)
	}
	if len(versions) != 1 || versions[0].Content != "第一版" {
		t.Fatalf("被拒请求不得改变内容，实际 %+v", versions)
	}
	enr, err := e.st.GetEnrollment(ctx, a.ID, "S01")
	if err != nil {
		t.Fatalf("读取状态行失败: %v", err)
	}
	if enr.CurrentVersionNo != 1 {
		t.Fatalf("被拒请求不得推进版本号，实际 %d", enr.CurrentVersionNo)
	}
}

// TestSubmitIdempotentReplayThreeTimes 验证重放三次仍只有一个版本、一条事件。
func TestSubmitIdempotentReplayThreeTimes(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	a := e.createPublished(t)
	svc := service.NewSubmissionService(e.st, e.clk)
	s01 := e.user(t, "S01")

	for i := 0; i < 3; i++ {
		res, err := svc.Submit(ctx, s01, a.ID, submitAt("同一份作业", "req-same", 0))
		if err != nil {
			t.Fatalf("第 %d 次请求失败: %v", i+1, err)
		}
		if res.Version.VersionNo != 1 {
			t.Fatalf("第 %d 次请求应返回 v1，实际 v%d", i+1, res.Version.VersionNo)
		}
		if i > 0 && !res.Replayed {
			t.Fatalf("第 %d 次请求应标记 Replayed", i+1)
		}
	}

	versions, err := e.st.ListSubmissions(ctx, a.ID, "S01")
	if err != nil {
		t.Fatalf("查询版本失败: %v", err)
	}
	if len(versions) != 1 {
		t.Fatalf("重放三次应只有 1 个版本，实际 %d", len(versions))
	}
	var events int
	if err := e.st.DB().QueryRow(
		`SELECT COUNT(1) FROM events WHERE assignment_id = ? AND student_id = 'S01' AND type = 'submitted'`,
		a.ID).Scan(&events); err != nil {
		t.Fatalf("统计事件失败: %v", err)
	}
	if events != 1 {
		t.Fatalf("重放三次应只有 1 条事件，实际 %d", events)
	}
}

// TestConcurrentSameRequestIDReplays 并发提交同一个 request_id：
// 只能落一个版本，其余请求按重放返回同一版本，不允许出现 v2。
func TestConcurrentSameRequestIDReplays(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	a := e.createPublished(t)
	svc := service.NewSubmissionService(e.st, e.clk)
	s01 := e.user(t, "S01")

	const n = 5
	var wg sync.WaitGroup
	results := make([]service.SubmitResult, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = svc.Submit(ctx, s01, a.ID, submitAt("并发同一请求", "req-burst", 0))
		}(i)
	}
	wg.Wait()

	newVersions := 0
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("并发重放第 %d 个请求失败: %v", i+1, errs[i])
		}
		if results[i].Version.VersionNo != 1 {
			t.Fatalf("所有请求都应指向 v1，第 %d 个为 v%d", i+1, results[i].Version.VersionNo)
		}
		if !results[i].Replayed {
			newVersions++
		}
	}
	if newVersions != 1 {
		t.Fatalf("应恰好 1 个请求真正落库，实际 %d", newVersions)
	}

	versions, err := e.st.ListSubmissions(ctx, a.ID, "S01")
	if err != nil {
		t.Fatalf("查询版本失败: %v", err)
	}
	if len(versions) != 1 {
		t.Fatalf("并发重放不得分叉，实际 %d 个版本", len(versions))
	}
}

// TestConcurrentDifferentRequestsNoFork 并发提交不同 request_id、同一 base_version：
// 只有一个成功，其余 409；最终只存在 v1，不存在 v2。
func TestConcurrentDifferentRequestsNoFork(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	a := e.createPublished(t)
	svc := service.NewSubmissionService(e.st, e.clk)
	s01 := e.user(t, "S01")

	const n = 5
	var wg sync.WaitGroup
	results := make([]service.SubmitResult, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = svc.Submit(ctx, s01, a.ID, submitAt("并发抢版本", "req-"+string(rune('a'+i)), 0))
		}(i)
	}
	wg.Wait()

	success := 0
	for i := 0; i < n; i++ {
		if errs[i] == nil {
			success++
			if results[i].Version.VersionNo != 1 {
				t.Fatalf("成功请求应落 v1，实际 v%d", results[i].Version.VersionNo)
			}
			continue
		}
		switch code(t, errs[i]) {
		case "stale_version", "version_conflict":
			// 两种都是"被并发抢先"的合法拒绝
		default:
			t.Fatalf("并发失败应为 stale_version/version_conflict，实际 %v", errs[i])
		}
	}
	if success != 1 {
		t.Fatalf("应恰好 1 个请求成功，实际 %d", success)
	}

	versions, err := e.st.ListSubmissions(ctx, a.ID, "S01")
	if err != nil {
		t.Fatalf("查询版本失败: %v", err)
	}
	if len(versions) != 1 {
		t.Fatalf("并发不同请求不得分叉，实际 %d 个版本", len(versions))
	}
	enr, err := e.st.GetEnrollment(ctx, a.ID, "S01")
	if err != nil {
		t.Fatalf("读取状态行失败: %v", err)
	}
	if enr.CurrentVersionNo != 1 || enr.State != store.StateSubmitted {
		t.Fatalf("状态行异常: %+v", enr)
	}
}
