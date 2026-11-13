package service_test

import (
	"context"
	"sync"
	"testing"

	"onlineclass/internal/service"
	"onlineclass/internal/store"
)

// TestReviewAwardsPointsOnceAndOnlyOnce 首次通过 +10；重放不重复发奖。
func TestReviewAwardsPointsOnceAndOnlyOnce(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	a := e.createPublished(t)
	subs := service.NewSubmissionService(e.st, e.clk)
	revs := service.NewReviewService(e.st, e.clk)
	teacher := e.user(t, "T01")

	sub, err := subs.Submit(ctx, e.user(t, "S01"), a.ID, submitInput("答案", "req-1"))
	if err != nil {
		t.Fatalf("提交失败: %v", err)
	}

	res, err := revs.Review(ctx, teacher, a.ID, reviewInput(sub.Version.ID, store.ReviewPass, "很好"))
	if err != nil {
		t.Fatalf("批阅失败: %v", err)
	}
	if !res.PointsAwarded || res.Points != store.PointsFirstPass {
		t.Fatalf("首次通过应发 10 分，实际 awarded=%v points=%d", res.PointsAwarded, res.Points)
	}

	// 重放同一结论：不发奖、不加分
	replay, err := revs.Review(ctx, teacher, a.ID, reviewInput(sub.Version.ID, store.ReviewPass, "很好"))
	if err != nil {
		t.Fatalf("重放失败: %v", err)
	}
	if replay.PointsAwarded || replay.Points != 0 {
		t.Fatalf("重放不应发奖，实际 awarded=%v points=%d", replay.PointsAwarded, replay.Points)
	}

	total, err := e.st.TotalPoints(ctx, "S01")
	if err != nil {
		t.Fatalf("统计总分失败: %v", err)
	}
	if total != store.PointsFirstPass {
		t.Fatalf("总分应仍为 10，实际 %d", total)
	}
	entries, err := e.st.ListPointsByAssignment(ctx, a.ID)
	if err != nil {
		t.Fatalf("查询流水失败: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("应只有 1 条流水，实际 %d", len(entries))
	}
}

// TestSubmitAndRejectAwardNothing 提交与退回都不发奖；退回后重交通过才发。
func TestSubmitAndRejectAwardNothing(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	a := e.createPublished(t)
	subs := service.NewSubmissionService(e.st, e.clk)
	revs := service.NewReviewService(e.st, e.clk)
	teacher := e.user(t, "T01")
	s01 := e.user(t, "S01")

	v1, err := subs.Submit(ctx, s01, a.ID, submitAt("第一版", "req-1", 0))
	if err != nil {
		t.Fatalf("提交失败: %v", err)
	}
	if total, _ := e.st.TotalPoints(ctx, "S01"); total != 0 {
		t.Fatalf("提交不应发奖，总分 %d", total)
	}

	if _, err := revs.Review(ctx, teacher, a.ID, reviewInput(v1.Version.ID, store.ReviewReject, "补充测试")); err != nil {
		t.Fatalf("退回失败: %v", err)
	}
	if total, _ := e.st.TotalPoints(ctx, "S01"); total != 0 {
		t.Fatalf("退回不应发奖，总分 %d", total)
	}

	v2, err := subs.Submit(ctx, s01, a.ID, submitAt("第二版", "req-2", 1))
	if err != nil {
		t.Fatalf("重交失败: %v", err)
	}
	res, err := revs.Review(ctx, teacher, a.ID, reviewInput(v2.Version.ID, store.ReviewPass, "通过"))
	if err != nil {
		t.Fatalf("批阅失败: %v", err)
	}
	if !res.PointsAwarded {
		t.Fatal("退回后重交通过应发奖")
	}
	if total, _ := e.st.TotalPoints(ctx, "S01"); total != 10 {
		t.Fatalf("总分应为 10，实际 %d", total)
	}
	// 另一位学生不受影响
	if total, _ := e.st.TotalPoints(ctx, "S02"); total != 0 {
		t.Fatalf("他人积分不应变化，实际 %d", total)
	}
}

// TestConcurrentPassAwardsOnce 同一版本被并发批阅（多为客户端重试）：
// 全部请求都成功（同结论重放），但积分只发一次。
func TestConcurrentPassAwardsOnce(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	a := e.createPublished(t)
	subs := service.NewSubmissionService(e.st, e.clk)
	revs := service.NewReviewService(e.st, e.clk)
	teacher := e.user(t, "T01")

	sub, err := subs.Submit(ctx, e.user(t, "S01"), a.ID, submitInput("答案", "req-1"))
	if err != nil {
		t.Fatalf("提交失败: %v", err)
	}

	const n = 5
	var wg sync.WaitGroup
	results := make([]service.ReviewResult, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = revs.Review(ctx, teacher, a.ID, reviewInput(sub.Version.ID, store.ReviewPass, "通过"))
		}(i)
	}
	wg.Wait()

	awarded := 0
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("并发批阅第 %d 个失败: %v", i+1, errs[i])
		}
		if results[i].PointsAwarded {
			awarded++
		}
	}
	if awarded != 1 {
		t.Fatalf("应恰好 1 个请求发奖，实际 %d", awarded)
	}

	entries, err := e.st.ListPointsByAssignment(ctx, a.ID)
	if err != nil {
		t.Fatalf("查询流水失败: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("并发下应只有 1 条流水，实际 %d", len(entries))
	}
	if total, _ := e.st.TotalPoints(ctx, "S01"); total != 10 {
		t.Fatalf("总分应为 10，实际 %d", total)
	}
}
