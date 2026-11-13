package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"onlineclass/internal/clock"
	"onlineclass/internal/httpapi"
	"onlineclass/internal/store"
)

type testEnv struct {
	ts *httptest.Server
	st *store.Store
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	if err := st.Seed(); err != nil {
		t.Fatalf("种子失败: %v", err)
	}
	// P01 已发布作业（带快照）；P02 作业用于验证跨班 404。
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := st.DB().Exec(q, args...); err != nil {
			t.Fatalf("准备数据失败: %v", err)
		}
	}
	exec(`INSERT INTO assignments (id, class_id, teacher_id, title, requirement, deadline_at, state,
		published_at, snapshot_title, snapshot_requirement, snapshot_deadline_at, snapshot_by, snapshot_at, created_at)
		VALUES ('HW1','P01','T01','草稿标题','草稿要求','2026-12-01T00:00:00Z','PUBLISHED',
		'2026-11-13T00:00:00Z','快照标题','快照要求','2026-12-01T00:00:00Z','T01','2026-11-13T00:00:00Z','2026-11-13T00:00:00Z')`)
	exec(`INSERT INTO assignments (id, class_id, teacher_id, title, requirement, deadline_at, state, created_at)
		VALUES ('HW2','P02','T01','外班作业','外班要求','2026-12-01T00:00:00Z','DRAFT','2026-11-13T00:00:00Z')`)
	// P02 已发布作业：验证外班学生能看到"自己班"的作业，但看不到 P01 的。
	exec(`INSERT INTO assignments (id, class_id, teacher_id, title, requirement, deadline_at, state,
		published_at, snapshot_title, snapshot_requirement, snapshot_deadline_at, snapshot_by, snapshot_at, created_at)
		VALUES ('HW3','P02','T01','外班已发布','外班要求','2026-12-01T00:00:00Z','PUBLISHED',
		'2026-11-13T00:00:00Z','外班已发布','外班要求','2026-12-01T00:00:00Z','T01','2026-11-13T00:00:00Z','2026-11-13T00:00:00Z')`)
	// P01 草稿：学生与助教不可见，教师可见。
	exec(`INSERT INTO assignments (id, class_id, teacher_id, title, requirement, deadline_at, state, created_at)
		VALUES ('HW4','P01','T01','草稿作业','草稿要求','2026-12-01T00:00:00Z','DRAFT','2026-11-13T00:00:00Z')`)

	app := httpapi.New(st, clock.New(0), time.Hour)
	ts := httptest.NewServer(app.Handler())
	t.Cleanup(ts.Close)
	return &testEnv{ts: ts, st: st}
}

// login 建立带会话 Cookie 的客户端；不自动跟随重定向，便于断言状态码。
func (e *testEnv) login(t *testing.T, userID, password string) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("创建 cookie jar 失败: %v", err)
	}
	c := &http.Client{
		Jar: jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := c.PostForm(e.ts.URL+"/login", url.Values{"user_id": {userID}, "password": {password}})
	if err != nil {
		t.Fatalf("登录请求失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("%s 登录应 303 跳转，实际 %d", userID, resp.StatusCode)
	}
	return c
}

func (e *testEnv) anonymous() *http.Client {
	return &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

func doJSON(t *testing.T, c *http.Client, method, target string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(method, target, nil)
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	var body map[string]any
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		_ = json.NewDecoder(resp.Body).Decode(&body)
	}
	return resp.StatusCode, body
}

func TestLoginRejectsWrongPassword(t *testing.T) {
	env := newTestEnv(t)
	c := env.anonymous()
	resp, err := c.PostForm(env.ts.URL+"/login", url.Values{"user_id": {"S01"}, "password": {"wrong"}})
	if err != nil {
		t.Fatalf("登录请求失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("错误口令应 401，实际 %d", resp.StatusCode)
	}
	if code, _ := doJSON(t, c, http.MethodGet, env.ts.URL+"/api/me"); code != http.StatusUnauthorized {
		t.Fatalf("失败登录不应建立会话，/api/me 应 401，实际 %d", code)
	}
}

func TestLoginRejectsUnknownUserWithSameMessage(t *testing.T) {
	env := newTestEnv(t)
	c := env.anonymous()
	resp, err := c.PostForm(env.ts.URL+"/login", url.Values{"user_id": {"NOPE"}, "password": {"demo1234"}})
	if err != nil {
		t.Fatalf("登录请求失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("未知账号应 401，实际 %d", resp.StatusCode)
	}
}

func TestMeUsesSessionIdentityOnly(t *testing.T) {
	env := newTestEnv(t)
	c := env.login(t, "S01", store.DemoPassword)

	code, body := doJSON(t, c, http.MethodGet, env.ts.URL+"/api/me?user_id=T01&id=T01")
	if code != http.StatusOK {
		t.Fatalf("/api/me 应 200，实际 %d", code)
	}
	if body["id"] != "S01" {
		t.Fatalf("身份应来自会话 S01，实际 %v", body["id"])
	}
	if body["class_id"] != "P01" {
		t.Fatalf("班级应来自会话，实际 %v", body["class_id"])
	}
}

func TestUnauthenticatedAccess(t *testing.T) {
	env := newTestEnv(t)
	c := env.anonymous()

	if code, _ := doJSON(t, c, http.MethodGet, env.ts.URL+"/api/me"); code != http.StatusUnauthorized {
		t.Fatalf("未登录 /api/me 应 401，实际 %d", code)
	}
	if code, _ := doJSON(t, c, http.MethodGet, env.ts.URL+"/api/assignments/HW1"); code != http.StatusUnauthorized {
		t.Fatalf("未登录查看作业应 401，实际 %d", code)
	}
	resp, err := c.Get(env.ts.URL + "/")
	if err != nil {
		t.Fatalf("请求首页失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/login" {
		t.Fatalf("未登录访问首页应跳转登录，实际 %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
}

func TestAssignmentVisibility(t *testing.T) {
	env := newTestEnv(t)

	student := env.login(t, "S01", store.DemoPassword)
	code, body := doJSON(t, student, http.MethodGet, env.ts.URL+"/api/assignments/HW1")
	if code != http.StatusOK {
		t.Fatalf("本班学生应可见作业，实际 %d", code)
	}
	if body["title"] != "快照标题" || body["requirement"] != "快照要求" {
		t.Fatalf("已发布作业应返回快照内容，实际 %v / %v", body["title"], body["requirement"])
	}

	outsider := env.login(t, "X01", store.DemoPassword)
	if code, _ := doJSON(t, outsider, http.MethodGet, env.ts.URL+"/api/assignments/HW1"); code != http.StatusNotFound {
		t.Fatalf("外班访问 P01 作业应 404，实际 %d", code)
	}
	if code, _ := doJSON(t, outsider, http.MethodGet, env.ts.URL+"/api/assignments/HW3"); code != http.StatusOK {
		t.Fatalf("外班学生应能看到自己班（P02）已发布作业，实际 %d", code)
	}

	teacher := env.login(t, "T01", store.DemoPassword)
	if code, _ := doJSON(t, teacher, http.MethodGet, env.ts.URL+"/api/assignments/HW1"); code != http.StatusOK {
		t.Fatalf("教师查看本班作业应 200，实际 %d", code)
	}
	if code, _ := doJSON(t, teacher, http.MethodGet, env.ts.URL+"/api/assignments/HW2"); code != http.StatusNotFound {
		t.Fatalf("教师跨班查看应 404，实际 %d", code)
	}

	// 草稿可见性：学生与助教视为不存在，教师可见。
	if code, _ := doJSON(t, student, http.MethodGet, env.ts.URL+"/api/assignments/HW4"); code != http.StatusNotFound {
		t.Fatalf("学生不应看到草稿，实际 %d", code)
	}
	assistant := env.login(t, "A01", store.DemoPassword)
	if code, _ := doJSON(t, assistant, http.MethodGet, env.ts.URL+"/api/assignments/HW4"); code != http.StatusNotFound {
		t.Fatalf("助教不应看到草稿，实际 %d", code)
	}
	if code, _ := doJSON(t, teacher, http.MethodGet, env.ts.URL+"/api/assignments/HW4"); code != http.StatusOK {
		t.Fatalf("教师应能看到本班草稿，实际 %d", code)
	}

	if code, _ := doJSON(t, student, http.MethodGet, env.ts.URL+"/api/assignments/NOT-EXIST"); code != http.StatusNotFound {
		t.Fatalf("不存在的作业应 404，实际 %d", code)
	}
}

func TestStudentDataIsolation(t *testing.T) {
	env := newTestEnv(t)

	self := env.login(t, "S01", store.DemoPassword)
	if code, body := doJSON(t, self, http.MethodGet, env.ts.URL+"/api/students/S01"); code != http.StatusOK {
		t.Fatalf("查看自己应 200，实际 %d", code)
	} else if body["id"] != "S01" {
		t.Fatalf("返回对象错误: %v", body["id"])
	}
	if code, _ := doJSON(t, self, http.MethodGet, env.ts.URL+"/api/students/S02"); code != http.StatusForbidden {
		t.Fatalf("学生查看同学应 403，实际 %d", code)
	}

	teacher := env.login(t, "T01", store.DemoPassword)
	if code, _ := doJSON(t, teacher, http.MethodGet, env.ts.URL+"/api/students/S02"); code != http.StatusOK {
		t.Fatalf("教师查看本班学生应 200，实际 %d", code)
	}

	assistant := env.login(t, "A01", store.DemoPassword)
	if code, _ := doJSON(t, assistant, http.MethodGet, env.ts.URL+"/api/students/S02"); code != http.StatusOK {
		t.Fatalf("助教查看本班学生状态应 200，实际 %d", code)
	}

	outsider := env.login(t, "X01", store.DemoPassword)
	if code, _ := doJSON(t, outsider, http.MethodGet, env.ts.URL+"/api/students/S01"); code != http.StatusNotFound {
		t.Fatalf("外班查看学生应 404，实际 %d", code)
	}
}

func TestWriteEndpointRoleMatrix(t *testing.T) {
	env := newTestEnv(t)
	teacher := env.login(t, "T01", store.DemoPassword)
	assistant := env.login(t, "A01", store.DemoPassword)
	student := env.login(t, "S01", store.DemoPassword)
	outsider := env.login(t, "X01", store.DemoPassword)

	post := func(c *http.Client, id string) int {
		code, _ := doJSON(t, c, http.MethodPost, env.ts.URL+"/api/assignments/"+id)
		return code
	}

	cases := []struct {
		name   string
		client *http.Client
		id     string
		want   int
	}{
		{"学生提交自己作业", student, "HW1/submissions", http.StatusNotImplemented},
		{"教师提交作业", teacher, "HW1/submissions", http.StatusForbidden},
		{"助教提交作业", assistant, "HW1/submissions", http.StatusForbidden},

		{"教师批阅", teacher, "HW1/reviews", http.StatusNotImplemented},
		{"助教批阅", assistant, "HW1/reviews", http.StatusForbidden},
		{"学生批阅", student, "HW1/reviews", http.StatusForbidden},
		{"外班教师批阅本班作业", teacher, "HW2/reviews", http.StatusNotFound},

		{"教师批准补交", teacher, "HW1/extensions", http.StatusNotImplemented},
		{"助教批准补交", assistant, "HW1/extensions", http.StatusForbidden},
		{"学生批准补交", student, "HW1/extensions", http.StatusForbidden},

		{"助教记录催交", assistant, "HW1/reminders", http.StatusNotImplemented},
		{"教师记录催交", teacher, "HW1/reminders", http.StatusForbidden},
		{"学生记录催交", student, "HW1/reminders", http.StatusForbidden},

		{"外班学生提交本班作业", outsider, "HW1/submissions", http.StatusNotFound},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := post(c.client, c.id); got != c.want {
				t.Fatalf("%s 期望 %d，实际 %d", c.name, c.want, got)
			}
		})
	}
}

func TestLogoutInvalidatesSession(t *testing.T) {
	env := newTestEnv(t)
	c := env.login(t, "T01", store.DemoPassword)
	if code, _ := doJSON(t, c, http.MethodGet, env.ts.URL+"/api/me"); code != http.StatusOK {
		t.Fatalf("登录后应可访问，实际 %d", code)
	}
	resp, err := c.PostForm(env.ts.URL+"/logout", url.Values{})
	if err != nil {
		t.Fatalf("退出请求失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("退出应 303，实际 %d", resp.StatusCode)
	}
	if code, _ := doJSON(t, c, http.MethodGet, env.ts.URL+"/api/me"); code != http.StatusUnauthorized {
		t.Fatalf("退出后 /api/me 应 401，实际 %d", code)
	}
}

func TestLoginPageRenders(t *testing.T) {
	env := newTestEnv(t)
	resp, err := env.anonymous().Get(env.ts.URL + "/login")
	if err != nil {
		t.Fatalf("请求登录页失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("登录页应 200，实际 %d", resp.StatusCode)
	}
}

func TestHealthz(t *testing.T) {
	env := newTestEnv(t)
	code, body := doJSON(t, env.anonymous(), http.MethodGet, env.ts.URL+"/healthz")
	if code != http.StatusOK {
		t.Fatalf("健康检查应 200，实际 %d", code)
	}
	if body["status"] != "ok" {
		t.Fatalf("健康检查状态错误: %v", body["status"])
	}
}
