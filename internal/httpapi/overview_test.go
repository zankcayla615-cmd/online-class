package httpapi_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"onlineclass/internal/store"
)

func boardPath(env *testEnv, id string) string {
	return env.ts.URL + "/api/assignments/" + id + "/board"
}

func summaryPath(env *testEnv, id string) string {
	return env.ts.URL + "/api/assignments/" + id + "/summary"
}

func TestBoardOverHTTP(t *testing.T) {
	env := newTestEnv(t)
	teacher := env.login(t, "T01", store.DemoPassword)
	assistant := env.login(t, "A01", store.DemoPassword)
	student := env.login(t, "S01", store.DemoPassword)
	outsider := env.login(t, "X01", store.DemoPassword)
	id := publishAssignment(t, env, teacher)

	sub := submitOne(t, env, student, id, "我的答案", "req-1", 0)
	if code, _ := doJSONBody(t, teacher, http.MethodPost, env.ts.URL+"/api/assignments/"+id+"/reviews",
		jsonBody(t, reviewBody(versionID(t, sub), store.ReviewPass, "通过"))); code != http.StatusCreated {
		t.Fatalf("批阅失败: %d", code)
	}

	code, board := doJSONBody(t, assistant, http.MethodGet, boardPath(env, id), nil)
	if code != http.StatusOK {
		t.Fatalf("助教看板应 200，实际 %d %v", code, board)
	}
	counts, _ := board["counts"].(map[string]any)
	if counts["passed"] != float64(1) || counts["not_submitted"] != float64(4) || counts["total"] != float64(5) {
		t.Fatalf("四类计数不符: %v", counts)
	}
	if board["completion_rate"] != 0.2 {
		t.Fatalf("完成率应为 0.2，实际 %v", board["completion_rate"])
	}
	students, _ := board["students"].([]any)
	if len(students) != 5 {
		t.Fatalf("应返回 5 名学生，实际 %d", len(students))
	}
	// 看板不得携带答案正文
	raw, _ := json.Marshal(board)
	if containsKey(raw, "content") {
		t.Fatalf("看板不应包含正文: %s", raw)
	}

	if code, _ := doJSONBody(t, teacher, http.MethodGet, boardPath(env, id), nil); code != http.StatusOK {
		t.Fatalf("教师看板应 200，实际 %d", code)
	}
	if code, _ := doJSONBody(t, student, http.MethodGet, boardPath(env, id), nil); code != http.StatusForbidden {
		t.Fatalf("学生看板应 403，实际 %d", code)
	}
	if code, _ := doJSONBody(t, outsider, http.MethodGet, boardPath(env, id), nil); code != http.StatusForbidden {
		t.Fatalf("外班学生看板应 403（不泄露存在性），实际 %d", code)
	}
}

func TestSummaryOverHTTP(t *testing.T) {
	env := newTestEnv(t)
	teacher := env.login(t, "T01", store.DemoPassword)
	assistant := env.login(t, "A01", store.DemoPassword)
	student := env.login(t, "S01", store.DemoPassword)
	id := publishAssignment(t, env, teacher)

	sub := submitOne(t, env, student, id, "我的答案", "req-1", 0)
	if code, _ := doJSONBody(t, teacher, http.MethodPost, env.ts.URL+"/api/assignments/"+id+"/reviews",
		jsonBody(t, reviewBody(versionID(t, sub), store.ReviewPass, "通过"))); code != http.StatusCreated {
		t.Fatalf("批阅失败: %d", code)
	}

	code, summary := doJSONBody(t, teacher, http.MethodGet, summaryPath(env, id), nil)
	if code != http.StatusOK {
		t.Fatalf("教师汇总应 200，实际 %d %v", code, summary)
	}
	if summary["read_only_stats"] != false {
		t.Fatalf("教师汇总 read_only_stats 应为 false: %v", summary)
	}
	if _, ok := summary["students"]; !ok {
		t.Fatalf("教师汇总应含学生明细: %v", summary)
	}
	if _, ok := summary["timeline"]; !ok {
		t.Fatalf("教师汇总应含时间线: %v", summary)
	}
	points, _ := summary["points"].([]any)
	if len(points) != 1 || summary["total_points"] != float64(10) {
		t.Fatalf("奖励流水应为 1 条 10 分: %v / %v", points, summary["total_points"])
	}
	timeline, _ := summary["timeline"].([]any)
	if len(timeline) < 2 {
		t.Fatalf("时间线至少含提交与批阅，实际 %v", timeline)
	}
	raw, _ := json.Marshal(summary)
	if containsKey(raw, "content") {
		t.Fatalf("汇总不应包含正文: %s", raw)
	}

	// 助教只读统计：无明细、无流水、无时间线
	code, stats := doJSONBody(t, assistant, http.MethodGet, summaryPath(env, id), nil)
	if code != http.StatusOK || stats["read_only_stats"] != true {
		t.Fatalf("助教汇总应 200 且 read_only_stats=true，实际 %d %v", code, stats)
	}
	for _, key := range []string{"students", "points", "timeline"} {
		if _, ok := stats[key]; ok {
			t.Fatalf("助教汇总不应包含 %s: %v", key, stats)
		}
	}
	if stats["counts"].(map[string]any)["passed"] != float64(1) {
		t.Fatalf("助教统计计数不符: %v", stats["counts"])
	}

	if code, _ := doJSONBody(t, student, http.MethodGet, summaryPath(env, id), nil); code != http.StatusForbidden {
		t.Fatalf("学生汇总应 403，实际 %d", code)
	}
}

func TestRemindersOverHTTP(t *testing.T) {
	env := newTestEnv(t)
	teacher := env.login(t, "T01", store.DemoPassword)
	assistant := env.login(t, "A01", store.DemoPassword)
	student := env.login(t, "S01", store.DemoPassword)
	id := publishAssignment(t, env, teacher)
	path := env.ts.URL + "/api/assignments/" + id + "/reminders"

	for i, note := range []string{"第一次电话未接", "第二次群里提醒"} {
		body := map[string]any{"student_id": "S02", "channel": store.ChannelPhone, "note": note}
		if i == 1 {
			body["channel"] = store.ChannelGroup
		}
		code, rem := doJSONBody(t, assistant, http.MethodPost, path, jsonBody(t, body))
		if code != http.StatusCreated {
			t.Fatalf("第 %d 次催交应 201，实际 %d %v", i+1, code, rem)
		}
		if rem["assistant_id"] != "A01" {
			t.Fatalf("催交记录应带助教编号: %v", rem)
		}
	}

	_, board := doJSONBody(t, assistant, http.MethodGet, boardPath(env, id), nil)
	reminders, _ := board["reminders"].([]any)
	if len(reminders) != 2 {
		t.Fatalf("看板应保留 2 条催交记录，实际 %d", len(reminders))
	}

	if code, _ := doJSONBody(t, teacher, http.MethodPost, path, jsonBody(t, map[string]any{
		"student_id": "S02", "channel": store.ChannelPhone, "note": "教师越权"})); code != http.StatusForbidden {
		t.Fatalf("教师记录催交应 403，实际 %d", code)
	}
	if code, _ := doJSONBody(t, student, http.MethodPost, path, jsonBody(t, map[string]any{
		"student_id": "S02", "channel": store.ChannelPhone, "note": "学生越权"})); code != http.StatusForbidden {
		t.Fatalf("学生记录催交应 403，实际 %d", code)
	}
	if code, _ := doJSONBody(t, assistant, http.MethodPost, path, jsonBody(t, map[string]any{
		"student_id": "S02", "channel": "wechat", "note": "非法方式"})); code != http.StatusBadRequest {
		t.Fatalf("非法方式应 400，实际 %d", code)
	}
	if code, _ := doJSONBody(t, assistant, http.MethodPost, path, jsonBody(t, map[string]any{
		"student_id": "S02", "channel": store.ChannelPhone, "note": "  "})); code != http.StatusBadRequest {
		t.Fatalf("空摘要应 400，实际 %d", code)
	}

	// 催交只追加：没有修改/删除路由
	if code, _ := doJSONBody(t, assistant, http.MethodPut, path, jsonBody(t, map[string]any{})); code != http.StatusMethodNotAllowed {
		t.Fatalf("修改催交应 405，实际 %d", code)
	}
	if code, _ := doJSONBody(t, assistant, http.MethodDelete, path, nil); code != http.StatusMethodNotAllowed {
		t.Fatalf("删除催交应 405，实际 %d", code)
	}
}

func containsKey(raw []byte, key string) bool {
	var probe map[string]any
	if err := json.Unmarshal(raw, &probe); err != nil {
		return false
	}
	_, ok := probe[key]
	return ok
}
