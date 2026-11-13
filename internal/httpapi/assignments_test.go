package httpapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"onlineclass/internal/store"
)

func jsonBody(t *testing.T, v any) *bytes.Reader {
	t.Helper()
	buf, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("序列化请求体失败: %v", err)
	}
	return bytes.NewReader(buf)
}

func doJSONBody(t *testing.T, c *http.Client, method, target string, body io.Reader) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(method, target, body)
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if resp.Header.Get("Content-Type") == "application/json; charset=utf-8" {
		_ = json.NewDecoder(resp.Body).Decode(&out)
	}
	return resp.StatusCode, out
}

func validPayload() map[string]any {
	return map[string]any{
		"title":       "第 4 章 事务与锁",
		"requirement": "实现带重试的提交接口，提交代码与测试",
		"deadline_at": time.Now().Add(72 * time.Hour).Format(time.RFC3339),
	}
}

func TestCreateAssignmentOverHTTP(t *testing.T) {
	env := newTestEnv(t)
	teacher := env.login(t, "T01", store.DemoPassword)

	code, body := doJSONBody(t, teacher, http.MethodPost, env.ts.URL+"/api/assignments", jsonBody(t, validPayload()))
	if code != http.StatusCreated {
		t.Fatalf("教师创建应 201，实际 %d %v", code, body)
	}
	if body["state"] != store.AssignmentDraft {
		t.Fatalf("新建应为草稿，实际 %v", body["state"])
	}
	if body["class_id"] != "P01" {
		t.Fatalf("班级应取自会话，实际 %v", body["class_id"])
	}

	student := env.login(t, "S01", store.DemoPassword)
	if code, _ := doJSONBody(t, student, http.MethodPost, env.ts.URL+"/api/assignments", jsonBody(t, validPayload())); code != http.StatusForbidden {
		t.Fatalf("学生创建应 403，实际 %d", code)
	}

	assistant := env.login(t, "A01", store.DemoPassword)
	if code, _ := doJSONBody(t, assistant, http.MethodPost, env.ts.URL+"/api/assignments", jsonBody(t, validPayload())); code != http.StatusForbidden {
		t.Fatalf("助教创建应 403，实际 %d", code)
	}
}

func TestCreateAssignmentValidationOverHTTP(t *testing.T) {
	env := newTestEnv(t)
	teacher := env.login(t, "T01", store.DemoPassword)

	past := validPayload()
	past["deadline_at"] = time.Now().Add(-time.Hour).Format(time.RFC3339)
	code, body := doJSONBody(t, teacher, http.MethodPost, env.ts.URL+"/api/assignments", jsonBody(t, past))
	if code != http.StatusBadRequest || body["code"] != "invalid_input" {
		t.Fatalf("过去截止时间应 400 invalid_input，实际 %d %v", code, body)
	}

	bad := validPayload()
	bad["deadline_at"] = "明天下午"
	if code, body := doJSONBody(t, teacher, http.MethodPost, env.ts.URL+"/api/assignments", jsonBody(t, bad)); code != http.StatusBadRequest || body["code"] != "invalid_input" {
		t.Fatalf("非法时间格式应 400，实际 %d %v", code, body)
	}

	unknown := validPayload()
	unknown["titlee"] = "拼错的字段"
	if code, body := doJSONBody(t, teacher, http.MethodPost, env.ts.URL+"/api/assignments", jsonBody(t, unknown)); code != http.StatusBadRequest || body["code"] != "invalid_input" {
		t.Fatalf("未知字段应 400，实际 %d %v", code, body)
	}
}

func TestPublishLocksContentOverHTTP(t *testing.T) {
	env := newTestEnv(t)
	ctx := t.Context()
	teacher := env.login(t, "T01", store.DemoPassword)

	payload := validPayload()
	code, created := doJSONBody(t, teacher, http.MethodPost, env.ts.URL+"/api/assignments", jsonBody(t, payload))
	if code != http.StatusCreated {
		t.Fatalf("创建失败: %d %v", code, created)
	}
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatal("创建响应缺少 id")
	}

	code, published := doJSONBody(t, teacher, http.MethodPost, env.ts.URL+"/api/assignments/"+id+"/publish", jsonBody(t, map[string]any{}))
	if code != http.StatusOK {
		t.Fatalf("发布应 200，实际 %d %v", code, published)
	}
	if published["state"] != store.AssignmentPublished {
		t.Fatalf("发布后状态错误: %v", published["state"])
	}
	if published["title"] != payload["title"] || published["requirement"] != payload["requirement"] {
		t.Fatalf("发布响应应展示快照内容，实际 %v / %v", published["title"], published["requirement"])
	}

	// 发布后修改 → 409 assignment_locked
	changed := validPayload()
	changed["title"] = "发布后偷偷改标题"
	code, body := doJSONBody(t, teacher, http.MethodPatch, env.ts.URL+"/api/assignments/"+id, jsonBody(t, changed))
	if code != http.StatusConflict || body["code"] != "assignment_locked" {
		t.Fatalf("发布后修改应 409 assignment_locked，实际 %d %v", code, body)
	}

	// 数据库内容与快照均未改变
	a, err := env.st.GetAssignment(ctx, id)
	if err != nil {
		t.Fatalf("读取作业失败: %v", err)
	}
	if a.Title != payload["title"] || a.Requirement != payload["requirement"] {
		t.Fatalf("被拒修改改变了内容: %s / %s", a.Title, a.Requirement)
	}

	// 学生看到的是快照内容
	student := env.login(t, "S01", store.DemoPassword)
	if code, view := doJSON(t, student, http.MethodGet, env.ts.URL+"/api/assignments/"+id); code != http.StatusOK {
		t.Fatalf("学生应可见已发布作业，实际 %d", code)
	} else if view["title"] != payload["title"] {
		t.Fatalf("学生视图应为快照标题，实际 %v", view["title"])
	}
}

func TestPublishPermissionAndRepeatOverHTTP(t *testing.T) {
	env := newTestEnv(t)
	teacher := env.login(t, "T01", store.DemoPassword)
	assistant := env.login(t, "A01", store.DemoPassword)
	student := env.login(t, "S01", store.DemoPassword)

	code, created := doJSONBody(t, teacher, http.MethodPost, env.ts.URL+"/api/assignments", jsonBody(t, validPayload()))
	if code != http.StatusCreated {
		t.Fatalf("创建失败: %d %v", code, created)
	}
	id, _ := created["id"].(string)

	if code, _ := doJSONBody(t, assistant, http.MethodPost, env.ts.URL+"/api/assignments/"+id+"/publish", jsonBody(t, map[string]any{})); code != http.StatusForbidden {
		t.Fatalf("助教发布应 403，实际 %d", code)
	}
	if code, _ := doJSONBody(t, student, http.MethodPost, env.ts.URL+"/api/assignments/"+id+"/publish", jsonBody(t, map[string]any{})); code != http.StatusForbidden {
		t.Fatalf("学生发布应 403，实际 %d", code)
	}

	if code, _ := doJSONBody(t, teacher, http.MethodPost, env.ts.URL+"/api/assignments/"+id+"/publish", jsonBody(t, map[string]any{})); code != http.StatusOK {
		t.Fatalf("教师发布应 200，实际 %d", code)
	}
	code, body := doJSONBody(t, teacher, http.MethodPost, env.ts.URL+"/api/assignments/"+id+"/publish", jsonBody(t, map[string]any{}))
	if code != http.StatusConflict || body["code"] != "already_published" {
		t.Fatalf("重复发布应 409 already_published，实际 %d %v", code, body)
	}
}

func TestDraftInvisibleToStudentOverHTTP(t *testing.T) {
	env := newTestEnv(t)
	teacher := env.login(t, "T01", store.DemoPassword)
	student := env.login(t, "S01", store.DemoPassword)

	code, created := doJSONBody(t, teacher, http.MethodPost, env.ts.URL+"/api/assignments", jsonBody(t, validPayload()))
	if code != http.StatusCreated {
		t.Fatalf("创建失败: %d %v", code, created)
	}
	id, _ := created["id"].(string)

	if code, _ := doJSON(t, student, http.MethodGet, env.ts.URL+"/api/assignments/"+id); code != http.StatusNotFound {
		t.Fatalf("学生不应看到草稿，实际 %d", code)
	}
	if code, _ := doJSON(t, teacher, http.MethodGet, env.ts.URL+"/api/assignments/"+id); code != http.StatusOK {
		t.Fatalf("教师应看到自己班草稿，实际 %d", code)
	}
}
