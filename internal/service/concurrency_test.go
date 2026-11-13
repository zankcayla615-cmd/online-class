package service_test

import (
	"context"
	"sync"
	"testing"

	"onlineclass/internal/service"
	"onlineclass/internal/store"
)

// TestConcurrentSubmitVsReview 让学生"基于 v1 更新到 v2"与教师"批阅 v1"真正并行。
//
// 允许两种结局：
//  1. 批阅先到：v1 通过 → 学生更新被拒（已通过），只有 1 个版本、发 10 分；
//  2. 提交先到：v2 落库 → 批阅 v1 得到 stale_version，2 个版本、0 分、状态不是 PASSED。
//
// 不允许：既有 v2 又 PASSED（旧版本被批成了通过），或同一版本出现两个结论。
func TestConcurrentSubmitVsReview(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	subs := service.NewSubmissionService(e.st, e.clk)
	revs := service.NewReviewService(e.st, e.clk)
	teacher := e.user(t, "T01")
	student := e.user(t, "S01")

	for round := 0; round < 10; round++ {
		a := e.createPublished(t)
		first, err := subs.Submit(ctx, student, a.ID, submitAt("第一版", "round-"+a.ID+"-v1", 0))
		if err != nil {
			t.Fatalf("第 %d 轮首次提交失败: %v", round+1, err)
		}
		versionID := first.Version.ID

		var (
			wg        sync.WaitGroup
			submitErr error
			submitRes service.SubmitResult
			reviewErr error
			reviewRes service.ReviewResult
		)
		wg.Add(2)
		go func() {
			defer wg.Done()
			submitRes, submitErr = subs.Submit(ctx, student, a.ID, submitAt("第二版", "round-"+a.ID+"-v2", 1))
		}()
		go func() {
			defer wg.Done()
			reviewRes, reviewErr = revs.Review(ctx, teacher, a.ID, reviewInput(versionID, store.ReviewPass, "通过"))
		}()
		wg.Wait()

		versions, err := e.st.ListSubmissions(ctx, a.ID, "S01")
		if err != nil {
			t.Fatalf("第 %d 轮查询版本失败: %v", round+1, err)
		}
		enr, err := e.st.GetEnrollment(ctx, a.ID, "S01")
		if err != nil {
			t.Fatalf("第 %d 轮查询状态失败: %v", round+1, err)
		}
		entries := ePoints(t, e, a.ID)

		if len(versions) > 2 {
			t.Fatalf("第 %d 轮版本数不应超过 2，实际 %d", round+1, len(versions))
		}

		if submitErr == nil {
			// 提交成功 → 版本必须真的推进到 v2
			if submitRes.Version.VersionNo != 2 || len(versions) != 2 {
				t.Fatalf("第 %d 轮提交成功但版本不匹配: res=%+v versions=%d", round+1, submitRes, len(versions))
			}
			// 此时旧版本批阅必须失败，且不能发奖
			if reviewErr == nil {
				t.Fatalf("第 %d 轮 v2 已存在时批阅 v1 不应成功（res=%+v）", round+1, reviewRes)
			}
			if code(t, reviewErr) != "stale_version" && code(t, reviewErr) != "version_conflict" {
				t.Fatalf("第 %d 轮批阅旧版本应报 stale_version，实际 %v", round+1, reviewErr)
			}
			if enr.State == store.StatePassed {
				t.Fatalf("第 %d 轮 v2 存在时状态不应为 PASSED", round+1)
			}
			if len(entries) != 0 {
				t.Fatalf("第 %d 轮未通过版本不应发奖，实际 %d 条流水", round+1, len(entries))
			}
			continue
		}

		// 提交失败 → 只能是批阅已经通过（或状态被并发推进）
		if code(t, submitErr) != "already_passed" && code(t, submitErr) != "version_conflict" {
			t.Fatalf("第 %d 轮提交失败原因不符: %v", round+1, submitErr)
		}
		if len(versions) != 1 {
			t.Fatalf("第 %d 轮提交失败时不应新增版本，实际 %d", round+1, len(versions))
		}
		if reviewErr == nil {
			if enr.State != store.StatePassed {
				t.Fatalf("第 %d 轮批阅通过后状态应为 PASSED，实际 %s", round+1, enr.State)
			}
			if len(entries) != 1 || entries[0].Points != store.PointsFirstPass {
				t.Fatalf("第 %d 轮通过应恰好发 %d 分，实际 %+v", round+1, store.PointsFirstPass, entries)
			}
		}
	}
}

// ePoints 是"该作业的积分流水"的简写，避免每个断言里重复写错误处理。
func ePoints(t *testing.T, e *svcEnv, assignmentID string) []store.PointsEntry {
	t.Helper()
	entries, err := e.st.ListPointsByAssignment(context.Background(), assignmentID)
	if err != nil {
		t.Fatalf("查询积分流水失败: %v", err)
	}
	return entries
}
