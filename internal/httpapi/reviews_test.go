package httpapi_test

import (
	"net/http"
	"testing"

	"onlineclass/internal/store"
)

func reviewBody(versionID, decision, feedback string) map[string]any {
	return map[string]any{"version_id": versionID, "decision": decision, "feedback": feedback}
}

// submitOne 让指定学生提交一版并返回响应体（含版本编号）。
func submitOne(t *testing.T, env *testEnv, student *http.Client, assignmentID, content, requestID string, baseVersion int) map[string]any {
	t.Helper()
	code, body := doJSONBody(t, student, http.MethodPost,
		env.ts.URL+"/api/assignments/"+assignmentID+"/submissions",
		jsonBody(t, submitBody(content, requestID, baseVersion)))
	if code != http.StatusCreated {
		t.Fatalf("提交 %s 失败: %d %v", requestID, code, body)
	}
	return body
}

func versionID(t *testing.T, body map[string]any) string {
	t.Helper()
	id, _ := body["id"].(string)
	if id == "" {
		t.Fatalf("提交响应缺少版本编号: %v", body)
	}
	return id
}

func TestReviewFlowOverHTTP(t *testing.T) {
	env := newTestEnv(t)
	teacher := env.login(t, "T01", store.DemoPassword)
	student := env.login(t, "S01", store.DemoPassword)
	id := publishAssignment(t, env, teacher)

	sub := submitOne(t, env, student, id, "我的答案", "req-1", 0)
	path := env.ts.URL + "/api/assignments/" + id + "/reviews"

	code, passed := doJSONBody(t, teacher, http.MethodPost, path, jsonBody(t, reviewBody(versionID(t, sub), store.ReviewPass, "写得很清楚")))
	if code != http.StatusCreated {
		t.Fatalf("通过批阅应 201，实际 %d %v", code, passed)
	}
	if passed["state"] != store.StatePassed || passed["replayed"] != false {
		t.Fatalf("通过后状态应为 PASSED 且非重放，实际 %v", passed)
	}

	code, replay := doJSONBody(t, teacher, http.MethodPost, path, jsonBody(t, reviewBody(versionID(t, sub), store.ReviewPass, "写得很清楚")))
	if code != http.StatusOK || replay["replayed"] != true {
		t.Fatalf("同结论重放应 200 且 replayed=true，实际 %d %v", code, replay)
	}
	if replay["id"] != passed["id"] {
		t.Fatalf("重放应返回首次结论，实际 %v vs %v", replay["id"], passed["id"])
	}

	code, conflict := doJSONBody(t, teacher, http.MethodPost, path, jsonBody(t, reviewBody(versionID(t, sub), store.ReviewReject, "改判")))
	if code != http.StatusConflict || conflict["code"] != "review_conflict" {
		t.Fatalf("同版本异结论应 409 review_conflict，实际 %d %v", code, conflict)
	}

	// 学生历史中应能看到结论
	code, history := doJSONBody(t, student, http.MethodGet, env.ts.URL+"/api/assignments/"+id+"/submissions", nil)
	if code != http.StatusOK {
		t.Fatalf("查询历史失败: %d", code)
	}
	if history["state"] != store.StatePassed {
		t.Fatalf("历史状态应为 PASSED，实际 %v", history["state"])
	}
	versions, _ := history["versions"].([]any)
	first, _ := versions[0].(map[string]any)
	review, _ := first["review"].(map[string]any)
	if review["decision"] != store.ReviewPass || review["feedback"] != "写得很清楚" {
		t.Fatalf("历史中应带批阅结论，实际 %v", first["review"])
	}
}

func TestReviewOldVersionOverHTTP(t *testing.T) {
	env := newTestEnv(t)
	teacher := env.login(t, "T01", store.DemoPassword)
	student := env.login(t, "S01", store.DemoPassword)
	id := publishAssignment(t, env, teacher)

	v1 := submitOne(t, env, student, id, "第一版", "req-1", 0)
	submitOne(t, env, student, id, "第二版", "req-2", 1)

	code, body := doJSONBody(t, teacher, http.MethodPost, env.ts.URL+"/api/assignments/"+id+"/reviews",
		jsonBody(t, reviewBody(versionID(t, v1), store.ReviewPass, "")))
	if code != http.StatusConflict || body["code"] != "stale_version" {
		t.Fatalf("批阅旧版本应 409 stale_version，实际 %d %v", code, body)
	}
	details, _ := body["details"].(map[string]any)
	if details["current_version_no"] != float64(2) || details["version_no"] != float64(1) {
		t.Fatalf("冲突详情应给出新旧版本，实际 %v", body["details"])
	}
}

func TestReviewRejectThenResubmitKeepsHistoryOverHTTP(t *testing.T) {
	env := newTestEnv(t)
	teacher := env.login(t, "T01", store.DemoPassword)
	student := env.login(t, "S01", store.DemoPassword)
	id := publishAssignment(t, env, teacher)
	path := env.ts.URL + "/api/assignments/" + id + "/reviews"

	v1 := submitOne(t, env, student, id, "第一版", "req-1", 0)
	code, rejected := doJSONBody(t, teacher, http.MethodPost, path, jsonBody(t, reviewBody(versionID(t, v1), store.ReviewReject, "补充单元测试")))
	if code != http.StatusCreated || rejected["state"] != store.StateReturned {
		t.Fatalf("退回应 201 且状态 RETURNED，实际 %d %v", code, rejected)
	}

	v2 := submitOne(t, env, student, id, "第二版", "req-2", 1)
	code, passed := doJSONBody(t, teacher, http.MethodPost, path, jsonBody(t, reviewBody(versionID(t, v2), store.ReviewPass, "通过")))
	if code != http.StatusCreated || passed["state"] != store.StatePassed {
		t.Fatalf("第二次批阅应 201 且 PASSED，实际 %d %v", code, passed)
	}

	code, history := doJSONBody(t, student, http.MethodGet, env.ts.URL+"/api/assignments/"+id+"/submissions", nil)
	if code != http.StatusOK {
		t.Fatalf("查询历史失败: %d", code)
	}
	versions, _ := history["versions"].([]any)
	if len(versions) != 2 {
		t.Fatalf("应保留两版，实际 %d", len(versions))
	}
	old, _ := versions[0].(map[string]any)
	oldReview, _ := old["review"].(map[string]any)
	if oldReview["decision"] != store.ReviewReject || oldReview["feedback"] != "补充单元测试" {
		t.Fatalf("旧版本意见应保留，实际 %v", old["review"])
	}
	latest, _ := versions[1].(map[string]any)
	latestReview, _ := latest["review"].(map[string]any)
	if latestReview["decision"] != store.ReviewPass {
		t.Fatalf("新版本意见应保留，实际 %v", latest["review"])
	}
}

func TestReviewPermissionsAndValidationOverHTTP(t *testing.T) {
	env := newTestEnv(t)
	teacher := env.login(t, "T01", store.DemoPassword)
	assistant := env.login(t, "A01", store.DemoPassword)
	student := env.login(t, "S01", store.DemoPassword)
	id := publishAssignment(t, env, teacher)
	sub := submitOne(t, env, student, id, "我的答案", "req-1", 0)
	vid := versionID(t, sub)
	path := env.ts.URL + "/api/assignments/" + id + "/reviews"

	if code, _ := doJSONBody(t, assistant, http.MethodPost, path, jsonBody(t, reviewBody(vid, store.ReviewPass, ""))); code != http.StatusForbidden {
		t.Fatalf("助教批阅应 403，实际 %d", code)
	}
	if code, _ := doJSONBody(t, student, http.MethodPost, path, jsonBody(t, reviewBody(vid, store.ReviewPass, ""))); code != http.StatusForbidden {
		t.Fatalf("学生批阅应 403，实际 %d", code)
	}
	// HW2 属于 P02：跨班一律 404
	if code, _ := doJSONBody(t, teacher, http.MethodPost, env.ts.URL+"/api/assignments/HW2/reviews", jsonBody(t, reviewBody(vid, store.ReviewPass, ""))); code != http.StatusNotFound {
		t.Fatalf("跨班批阅应 404，实际 %d", code)
	}
	if code, _ := doJSONBody(t, teacher, http.MethodPost, path, jsonBody(t, reviewBody(vid, store.ReviewReject, " "))); code != http.StatusBadRequest {
		t.Fatalf("退回缺少反馈应 400，实际 %d", code)
	}
	if code, _ := doJSONBody(t, teacher, http.MethodPost, path, jsonBody(t, map[string]any{"decision": store.ReviewPass})); code != http.StatusBadRequest {
		t.Fatalf("缺少 version_id 应 400，实际 %d", code)
	}
	if code, _ := doJSONBody(t, teacher, http.MethodPost, path, jsonBody(t, reviewBody("SUB-NOPE", store.ReviewPass, ""))); code != http.StatusNotFound {
		t.Fatalf("不存在的版本应 404，实际 %d", code)
	}
	// 未知字段整体拒绝，避免客户端误以为字段生效
	if code, _ := doJSONBody(t, teacher, http.MethodPost, path, jsonBody(t, map[string]any{"version_id": vid, "decision": store.ReviewPass, "score": 90})); code != http.StatusBadRequest {
		t.Fatalf("未知字段应 400，实际 %d", code)
	}
}
