package httpapi_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"onlineclass/internal/store"
)

// publishAssignment 通过接口创建并发布作业，返回作业编号。
func publishAssignment(t *testing.T, env *testEnv, teacher *http.Client) string {
	t.Helper()
	code, created := doJSONBody(t, teacher, http.MethodPost, env.ts.URL+"/api/assignments", jsonBody(t, validPayload()))
	if code != http.StatusCreated {
		t.Fatalf("创建作业失败: %d %v", code, created)
	}
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatal("创建响应缺少 id")
	}
	code, body := doJSONBody(t, teacher, http.MethodPost, env.ts.URL+"/api/assignments/"+id+"/publish", jsonBody(t, map[string]any{}))
	if code != http.StatusOK {
		t.Fatalf("发布作业失败: %d %v", code, body)
	}
	return id
}

func submitBody(content, requestID string, baseVersion int) map[string]any {
	return map[string]any{"content": content, "request_id": requestID, "base_version": baseVersion}
}

// seedPastDeadlineAssignment 直接写入一份已过期作业及状态行，用于验证逾期分支。
func seedPastDeadlineAssignment(t *testing.T, env *testEnv, id string) {
	t.Helper()
	past := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano)
	if _, err := env.st.DB().Exec(
		`INSERT INTO assignments
		 (id, class_id, teacher_id, title, requirement, deadline_at, state,
		  published_at, snapshot_title, snapshot_requirement, snapshot_deadline_at, snapshot_by, snapshot_at, created_at)
		 VALUES (?, 'P01', 'T01', '逾期作业', '要求', ?, 'PUBLISHED', ?, '逾期作业', '要求', ?, 'T01', ?, ?)`,
		id, past, past, past, past, past); err != nil {
		t.Fatalf("写入过期作业失败: %v", err)
	}
	if _, err := env.st.DB().Exec(
		`INSERT INTO enrollments (assignment_id, student_id, state, state_version, current_version_no)
		 SELECT ?, id, 'NOT_SUBMITTED', 0, 0 FROM users WHERE class_id = 'P01' AND role = 'student'`, id); err != nil {
		t.Fatalf("写入状态行失败: %v", err)
	}
}

func TestSubmitAndUpdateOverHTTP(t *testing.T) {
	env := newTestEnv(t)
	teacher := env.login(t, "T01", store.DemoPassword)
	student := env.login(t, "S01", store.DemoPassword)
	id := publishAssignment(t, env, teacher)
	path := env.ts.URL + "/api/assignments/" + id + "/submissions"

	code, v1 := doJSONBody(t, student, http.MethodPost, path, jsonBody(t, submitBody("第一版答案", "req-1", 0)))
	if code != http.StatusCreated {
		t.Fatalf("首次提交应 201，实际 %d %v", code, v1)
	}
	if v1["version_no"] != float64(1) || v1["state"] != store.StateSubmitted {
		t.Fatalf("首次提交应为 v1/SUBMITTED，实际 %v/%v", v1["version_no"], v1["state"])
	}
	if v1["replayed"] != false || v1["extension_applied"] != false {
		t.Fatalf("首次提交不应标记重放/补交: %v/%v", v1["replayed"], v1["extension_applied"])
	}

	code, v2 := doJSONBody(t, student, http.MethodPost, path, jsonBody(t, submitBody("第二版答案", "req-2", 1)))
	if code != http.StatusCreated || v2["version_no"] != float64(2) {
		t.Fatalf("更新应生成 v2，实际 %d %v", code, v2)
	}

	code, history := doJSONBody(t, student, http.MethodGet, path, nil)
	if code != http.StatusOK {
		t.Fatalf("查询历史应 200，实际 %d", code)
	}
	versions, _ := history["versions"].([]any)
	if len(versions) != 2 {
		t.Fatalf("应返回两个版本，实际 %d", len(versions))
	}
	first, _ := versions[0].(map[string]any)
	if first["content"] != "第一版答案" {
		t.Fatalf("第一版内容应保留，实际 %v", first["content"])
	}
	if history["current_version_no"] != float64(2) {
		t.Fatalf("当前版本号应为 2，实际 %v", history["current_version_no"])
	}
}

func TestSubmitIdempotentReplayOverHTTP(t *testing.T) {
	env := newTestEnv(t)
	teacher := env.login(t, "T01", store.DemoPassword)
	student := env.login(t, "S01", store.DemoPassword)
	id := publishAssignment(t, env, teacher)
	path := env.ts.URL + "/api/assignments/" + id + "/submissions"

	if code, _ := doJSONBody(t, student, http.MethodPost, path, jsonBody(t, submitBody("同一份答案", "req-same", 0))); code != http.StatusCreated {
		t.Fatalf("首次提交失败: %d", code)
	}

	// 同 request_id + 同内容：200 重放，version 仍为 1
	code, replay := doJSONBody(t, student, http.MethodPost, path, jsonBody(t, submitBody("同一份答案", "req-same", 0)))
	if code != http.StatusOK {
		t.Fatalf("幂等重放应 200，实际 %d %v", code, replay)
	}
	if replay["replayed"] != true || replay["version_no"] != float64(1) {
		t.Fatalf("重放应返回首次版本并标记 replayed，实际 %v", replay)
	}

	// 同 request_id + 异内容：409 idempotency_conflict
	code, conflict := doJSONBody(t, student, http.MethodPost, path, jsonBody(t, submitBody("偷偷改内容", "req-same", 1)))
	if code != http.StatusConflict || conflict["code"] != "idempotency_conflict" {
		t.Fatalf("同号异内容应 409 idempotency_conflict，实际 %d %v", code, conflict)
	}

	code, history := doJSONBody(t, student, http.MethodGet, path, nil)
	if code != http.StatusOK {
		t.Fatalf("查询历史失败: %d", code)
	}
	if history["current_version_no"] != float64(1) {
		t.Fatalf("重放与冲突都不得推进版本，实际 %v", history["current_version_no"])
	}
	if versions, _ := history["versions"].([]any); len(versions) != 1 {
		t.Fatalf("应只存在 1 个版本，实际 %d", len(versions))
	}
}

func TestSubmitStaleBaseVersionOverHTTP(t *testing.T) {
	env := newTestEnv(t)
	teacher := env.login(t, "T01", store.DemoPassword)
	student := env.login(t, "S01", store.DemoPassword)
	id := publishAssignment(t, env, teacher)
	path := env.ts.URL + "/api/assignments/" + id + "/submissions"

	if code, _ := doJSONBody(t, student, http.MethodPost, path, jsonBody(t, submitBody("第一版", "req-1", 0))); code != http.StatusCreated {
		t.Fatalf("首次提交失败: %d", code)
	}

	code, body := doJSONBody(t, student, http.MethodPost, path, jsonBody(t, submitBody("旧页面覆盖", "req-stale", 0)))
	if code != http.StatusConflict || body["code"] != "stale_version" {
		t.Fatalf("base_version 落后应 409 stale_version，实际 %d %v", code, body)
	}
	details, _ := body["details"].(map[string]any)
	if details["current_version_no"] != float64(1) || details["base_version"] != float64(0) {
		t.Fatalf("冲突详情应给出双方版本，实际 %v", body["details"])
	}

	code, history := doJSONBody(t, student, http.MethodGet, path, nil)
	if code != http.StatusOK {
		t.Fatalf("查询历史失败: %d", code)
	}
	versions, _ := history["versions"].([]any)
	if len(versions) != 1 {
		t.Fatalf("被拒请求不得新增版本，实际 %d", len(versions))
	}
	first, _ := versions[0].(map[string]any)
	if first["content"] != "第一版" {
		t.Fatalf("被拒请求不得改变内容，实际 %v", first["content"])
	}
}

func TestSubmitDeadlineAndPassedOverHTTP(t *testing.T) {
	env := newTestEnv(t)
	student := env.login(t, "S01", store.DemoPassword)

	seedPastDeadlineAssignment(t, env, "HW-PAST")
	code, body := doJSONBody(t, student, http.MethodPost, env.ts.URL+"/api/assignments/HW-PAST/submissions", jsonBody(t, submitBody("迟交", "req-late", 0)))
	if code != http.StatusConflict || body["code"] != "deadline_passed" {
		t.Fatalf("逾期提交应 409 deadline_passed，实际 %d %v", code, body)
	}

	teacher := env.login(t, "T01", store.DemoPassword)
	id := publishAssignment(t, env, teacher)
	if _, err := env.st.DB().Exec(
		`UPDATE enrollments SET state = 'PASSED' WHERE assignment_id = ? AND student_id = 'S01'`, id); err != nil {
		t.Fatalf("构造已通过状态失败: %v", err)
	}
	code, body = doJSONBody(t, student, http.MethodPost, env.ts.URL+"/api/assignments/"+id+"/submissions", jsonBody(t, submitBody("再交一次", "req-9", 1)))
	if code != http.StatusConflict || body["code"] != "already_passed" {
		t.Fatalf("已通过后提交应 409 already_passed，实际 %d %v", code, body)
	}
}

func TestSubmitPermissionsOverHTTP(t *testing.T) {
	env := newTestEnv(t)
	teacher := env.login(t, "T01", store.DemoPassword)
	assistant := env.login(t, "A01", store.DemoPassword)
	student := env.login(t, "S01", store.DemoPassword)
	outsider := env.login(t, "X01", store.DemoPassword)
	id := publishAssignment(t, env, teacher)

	paths := env.ts.URL + "/api/assignments/" + id + "/submissions"
	if code, _ := doJSONBody(t, teacher, http.MethodPost, paths, jsonBody(t, submitBody("教师提交", "req-t", 0))); code != http.StatusForbidden {
		t.Fatalf("教师提交应 403，实际 %d", code)
	}
	if code, _ := doJSONBody(t, assistant, http.MethodPost, paths, jsonBody(t, submitBody("助教提交", "req-a", 0))); code != http.StatusForbidden {
		t.Fatalf("助教提交应 403，实际 %d", code)
	}
	if code, _ := doJSONBody(t, outsider, http.MethodPost, paths, jsonBody(t, submitBody("外班提交", "req-x", 0))); code != http.StatusNotFound {
		t.Fatalf("外班提交应 404，实际 %d", code)
	}
	if code, _ := doJSONBody(t, student, http.MethodPost, paths, jsonBody(t, submitBody("内容", "", 0))); code != http.StatusBadRequest {
		t.Fatalf("缺少 request_id 应 400，实际 %d", code)
	}
	if code, _ := doJSONBody(t, student, http.MethodPost, paths, jsonBody(t, map[string]any{"content": "内容", "request_id": "r-1"})); code != http.StatusBadRequest {
		t.Fatalf("缺少 base_version 应 400，实际 %d", code)
	}
	if code, _ := doJSONBody(t, student, http.MethodPost, paths, jsonBody(t, map[string]any{"content": "x", "request_id": "r", "base_version": 0, "extra": 1})); code != http.StatusBadRequest {
		t.Fatalf("未知字段应 400，实际 %d", code)
	}
	if code, _ := doJSONBody(t, student, http.MethodPost, paths, jsonBody(t, submitBody("我的答案", "req-1", 0))); code != http.StatusCreated {
		t.Fatalf("学生提交应 201，实际 %d", code)
	}
}

func TestSubmissionListPermissionsOverHTTP(t *testing.T) {
	env := newTestEnv(t)
	teacher := env.login(t, "T01", store.DemoPassword)
	assistant := env.login(t, "A01", store.DemoPassword)
	student := env.login(t, "S01", store.DemoPassword)
	outsider := env.login(t, "X01", store.DemoPassword)
	id := publishAssignment(t, env, teacher)

	if code, _ := doJSONBody(t, student, http.MethodPost, env.ts.URL+"/api/assignments/"+id+"/submissions", jsonBody(t, submitBody("我的答案", "req-1", 0))); code != http.StatusCreated {
		t.Fatalf("学生提交失败: %d", code)
	}

	base := fmt.Sprintf("%s/api/assignments/%s/submissions", env.ts.URL, id)
	if code, _ := doJSONBody(t, teacher, http.MethodGet, base+"?student_id=S01", nil); code != http.StatusOK {
		t.Fatalf("教师查看本班提交应 200，实际 %d", code)
	}
	if code, _ := doJSONBody(t, assistant, http.MethodGet, base+"?student_id=S02", nil); code != http.StatusOK {
		t.Fatalf("助教查看本班学生应 200，实际 %d", code)
	}
	if code, _ := doJSONBody(t, student, http.MethodGet, base+"?student_id=S02", nil); code != http.StatusForbidden {
		t.Fatalf("学生查看同学应 403，实际 %d", code)
	}
	if code, _ := doJSONBody(t, teacher, http.MethodGet, base+"?student_id=X01", nil); code != http.StatusNotFound {
		t.Fatalf("跨班查看应 404，实际 %d", code)
	}
	if code, _ := doJSONBody(t, outsider, http.MethodGet, base, nil); code != http.StatusNotFound {
		t.Fatalf("外班访问作业提交应 404，实际 %d", code)
	}
}
