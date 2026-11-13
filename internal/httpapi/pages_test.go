package httpapi_test

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"onlineclass/internal/store"
)

func getPage(t *testing.T, c *http.Client, target string) (int, string) {
	t.Helper()
	resp, err := c.Get(target)
	if err != nil {
		t.Fatalf("请求页面失败: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取页面失败: %v", err)
	}
	return resp.StatusCode, string(body)
}

func postPage(t *testing.T, c *http.Client, target string, form url.Values) (int, string) {
	t.Helper()
	resp, err := c.PostForm(target, form)
	if err != nil {
		t.Fatalf("提交表单失败: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header.Get("Location") + "|" + string(body)
}

func TestPagesRequireLogin(t *testing.T) {
	env := newTestEnv(t)
	c := env.anonymous()

	if code, body := getPage(t, c, env.ts.URL+"/assignments"); code != http.StatusSeeOther || !strings.Contains(body, "/login") {
		t.Fatalf("未登录访问页面应跳登录，实际 %d %s", code, body)
	}
	if code, _ := getPage(t, c, env.ts.URL+"/"); code != http.StatusSeeOther {
		t.Fatalf("未登录访问首页应跳登录，实际 %d", code)
	}
}

func TestTeacherPublishFlowPages(t *testing.T) {
	env := newTestEnv(t)
	teacher := env.login(t, "T01", store.DemoPassword)

	code, body := getPage(t, teacher, env.ts.URL+"/assignments")
	if code != http.StatusOK || !strings.Contains(body, "新建作业草稿") {
		t.Fatalf("发布页应渲染新建表单，实际 %d", code)
	}
	if !strings.Contains(body, "草稿作业") || !strings.Contains(body, "快照标题") {
		t.Fatalf("发布页应列出数据库中的草稿与已发布作业")
	}

	deadline := time.Now().Add(48 * time.Hour).Format("2006-01-02T15:04")
	code, out := postPage(t, teacher, env.ts.URL+"/assignments", url.Values{
		"title":       {"页面新建的作业"},
		"requirement": {"请提交一段说明"},
		"deadline_at": {deadline},
	})
	if code != http.StatusSeeOther || !strings.Contains(out, "ok=created") {
		t.Fatalf("创建草稿应 303 到成功提示，实际 %d %s", code, out)
	}

	var id string
	if err := env.st.DB().QueryRow(`SELECT id FROM assignments WHERE title = '页面新建的作业'`).Scan(&id); err != nil {
		t.Fatalf("未写入草稿: %v", err)
	}

	code, out = postPage(t, teacher, env.ts.URL+"/assignments/"+id+"/publish", url.Values{})
	if code != http.StatusSeeOther || !strings.Contains(out, "ok=published") {
		t.Fatalf("发布应 303，实际 %d %s", code, out)
	}

	// 重复发布：错误页必须带错误码与中文原因
	code, body = getPage(t, teacher, env.ts.URL+"/assignments/"+id+"/review")
	if code != http.StatusOK || !strings.Contains(body, "批阅台") {
		t.Fatalf("批阅台应可访问，实际 %d", code)
	}
	code, body = postPage(t, teacher, env.ts.URL+"/assignments/"+id+"/publish", url.Values{})
	if code != http.StatusConflict || !strings.Contains(body, "already_published") || !strings.Contains(body, "作业已发布") {
		t.Fatalf("重复发布应展示可读错误，实际 %d %s", code, body)
	}
}

func TestStudentSubmitPageAndStaleVersion(t *testing.T) {
	env := newTestEnv(t)
	teacher := env.login(t, "T01", store.DemoPassword)
	student := env.login(t, "S01", store.DemoPassword)
	id := publishAssignment(t, env, teacher)

	code, body := getPage(t, student, env.ts.URL+"/assignments/"+id)
	if code != http.StatusOK || !strings.Contains(body, "实现带重试的提交接口") || !strings.Contains(body, "提交作业") {
		t.Fatalf("学生页应显示快照要求与提交表单，实际 %d", code)
	}

	code, out := postPage(t, student, env.ts.URL+"/assignments/"+id+"/submit", url.Values{
		"content":      {"页面提交的答案"},
		"request_id":   {"page-req-1"},
		"base_version": {"0"},
	})
	if code != http.StatusSeeOther || !strings.Contains(out, "ok=submitted") || !strings.Contains(out, "v=1") {
		t.Fatalf("首次提交应 303 并带版本号，实际 %d %s", code, out)
	}

	code, body = getPage(t, student, env.ts.URL+"/assignments/"+id)
	if code != http.StatusOK || !strings.Contains(body, "页面提交的答案") || !strings.Contains(body, "待批阅") {
		t.Fatalf("提交后页面应显示版本与状态，实际 %d", code)
	}

	// 旧页面（base_version=0）再提交：必须被拒绝并展示 stale_version
	code, body = postPage(t, student, env.ts.URL+"/assignments/"+id+"/submit", url.Values{
		"content":      {"旧页面覆盖"},
		"request_id":   {"page-req-2"},
		"base_version": {"0"},
	})
	if code != http.StatusConflict || !strings.Contains(body, "stale_version") || !strings.Contains(body, "旧版本") {
		t.Fatalf("旧版本提交应展示可读错误，实际 %d %s", code, body)
	}

	// 库中仍只有 v1，旧请求没有覆盖内容
	var n int
	if err := env.st.DB().QueryRow(`SELECT COUNT(1) FROM submission_versions WHERE assignment_id=? AND student_id='S01'`, id).Scan(&n); err != nil {
		t.Fatalf("统计版本失败: %v", err)
	}
	if n != 1 {
		t.Fatalf("被拒绝的旧请求不应新增版本，实际 %d", n)
	}
}

func TestReviewPageAndExtensionPage(t *testing.T) {
	env := newTestEnv(t)
	teacher := env.login(t, "T01", store.DemoPassword)
	student := env.login(t, "S01", store.DemoPassword)
	id := publishAssignment(t, env, teacher)
	sub := submitOne(t, env, student, id, "页面批阅用答案", "page-req-r", 0)

	code, body := getPage(t, teacher, env.ts.URL+"/assignments/"+id+"/review")
	if code != http.StatusOK || !strings.Contains(body, "页面批阅用答案") || !strings.Contains(body, "保存批阅") {
		t.Fatalf("批阅台应显示提交内容与批阅表单，实际 %d", code)
	}

	code, out := postPage(t, teacher, env.ts.URL+"/assignments/"+id+"/review", url.Values{
		"version_id": {versionID(t, sub)},
		"decision":   {"pass"},
		"feedback":   {"页面批阅：通过"},
		"request_id": {"page-review-1"},
	})
	if code != http.StatusSeeOther || !strings.Contains(out, "ok=reviewed") || !strings.Contains(out, "p=10") {
		t.Fatalf("通过批阅应 303 并提示发分，实际 %d %s", code, out)
	}

	code, body = getPage(t, teacher, env.ts.URL+"/assignments/"+id+"/review")
	if code != http.StatusOK || !strings.Contains(body, "已通过 1") || !strings.Contains(body, "页面批阅：通过") {
		t.Fatalf("批阅后页面应显示结论与计数，实际 %d", code)
	}

	// 同版本改判：一版一结论 → 409 review_conflict
	code, body = postPage(t, teacher, env.ts.URL+"/assignments/"+id+"/review", url.Values{
		"version_id": {versionID(t, sub)},
		"decision":   {"reject"},
		"feedback":   {"改判退回"},
		"request_id": {"page-review-2"},
	})
	if code != http.StatusConflict || !strings.Contains(body, "review_conflict") {
		t.Fatalf("改判应展示 review_conflict，实际 %d %s", code, body)
	}

	// 补交：晚于原截止（2026-12-01）即可
	code, out = postPage(t, teacher, env.ts.URL+"/assignments/"+id+"/extension", url.Values{
		"student_id":  {"S02"},
		"deadline_at": {"2026-12-05T10:00"},
		"reason":      {"因病住院，需要补交"},
	})
	if code != http.StatusSeeOther || !strings.Contains(out, "ok=extended") {
		t.Fatalf("补交应 303，实际 %d %s", code, out)
	}

	code, body = getPage(t, teacher, env.ts.URL+"/assignments/"+id+"/review")
	if code != http.StatusOK || !strings.Contains(body, "已批补交") {
		t.Fatalf("补交后批阅台应标注，实际 %d", code)
	}

	// 二次补交：一次性 → 409 extension_exists
	code, body = postPage(t, teacher, env.ts.URL+"/assignments/"+id+"/extension", url.Values{
		"student_id":  {"S02"},
		"deadline_at": {"2026-12-06T10:00"},
		"reason":      {"再次申请补交"},
	})
	if code != http.StatusConflict || !strings.Contains(body, "extension_exists") {
		t.Fatalf("二次补交应展示 extension_exists，实际 %d %s", code, body)
	}
}

func TestBoardPageAndReminderForm(t *testing.T) {
	env := newTestEnv(t)
	teacher := env.login(t, "T01", store.DemoPassword)
	assistant := env.login(t, "A01", store.DemoPassword)
	student := env.login(t, "S01", store.DemoPassword)
	id := publishAssignment(t, env, teacher)
	submitOne(t, env, student, id, "看板用答案", "page-req-b", 0)

	code, body := getPage(t, assistant, env.ts.URL+"/assignments/"+id+"/board")
	if code != http.StatusOK || !strings.Contains(body, "班级看板") || !strings.Contains(body, "记录本次催交") {
		t.Fatalf("助教看板应可访问且带催交表单，实际 %d", code)
	}
	if strings.Contains(body, "看板用答案") {
		t.Fatal("看板不应展示学生答案正文")
	}

	for i, note := range []string{"第一次电话未接", "第二次群里提醒"} {
		channel := "phone"
		if i == 1 {
			channel = "group"
		}
		code, out := postPage(t, assistant, env.ts.URL+"/assignments/"+id+"/reminder", url.Values{
			"student_id": {"S03"},
			"channel":    {channel},
			"note":       {note},
		})
		if code != http.StatusSeeOther || !strings.Contains(out, "ok=reminded") {
			t.Fatalf("第 %d 次催交应 303，实际 %d %s", i+1, code, out)
		}
	}

	code, body = getPage(t, assistant, env.ts.URL+"/assignments/"+id+"/board")
	if code != http.StatusOK || !strings.Contains(body, "催交记录（2 条）") || !strings.Contains(body, "第二次群里提醒") {
		t.Fatalf("看板应保留两次催交，实际 %d", code)
	}

	// 教师只读：能看板但不能记录催交
	code, body = getPage(t, teacher, env.ts.URL+"/assignments/"+id+"/board")
	if code != http.StatusOK {
		t.Fatalf("教师查看看板应 200，实际 %d", code)
	}
	if strings.Contains(body, "记录本次催交") {
		t.Fatal("教师不应看到催交表单")
	}
	code, body = postPage(t, teacher, env.ts.URL+"/assignments/"+id+"/reminder", url.Values{
		"student_id": {"S03"}, "channel": {"phone"}, "note": {"教师越权"},
	})
	if code != http.StatusForbidden || !strings.Contains(body, "forbidden_role") {
		t.Fatalf("教师催交应 403 页面，实际 %d %s", code, body)
	}

	// 学生看不到看板
	code, body = getPage(t, student, env.ts.URL+"/assignments/"+id+"/board")
	if code != http.StatusForbidden || !strings.Contains(body, "forbidden_role") {
		t.Fatalf("学生看板应 403，实际 %d", code)
	}
}

func TestSummaryPageTeacherVsAssistant(t *testing.T) {
	env := newTestEnv(t)
	teacher := env.login(t, "T01", store.DemoPassword)
	assistant := env.login(t, "A01", store.DemoPassword)
	student := env.login(t, "S01", store.DemoPassword)
	id := publishAssignment(t, env, teacher)

	sub := submitOne(t, env, student, id, "汇总用答案", "page-req-s", 0)
	if code, _ := doJSONBody(t, teacher, http.MethodPost, env.ts.URL+"/api/assignments/"+id+"/reviews",
		jsonBody(t, reviewBody(versionID(t, sub), store.ReviewPass, "通过"))); code != http.StatusCreated {
		t.Fatalf("准备批阅失败: %d", code)
	}

	code, body := getPage(t, teacher, env.ts.URL+"/assignments/"+id+"/summary")
	if code != http.StatusOK {
		t.Fatalf("教师汇总应 200，实际 %d", code)
	}
	for _, want := range []string{"学生最终状态", "奖励流水", "时间线（首次提交", "汇总用答案"} {
		if want == "汇总用答案" {
			if strings.Contains(body, want) {
				t.Fatal("汇总不应包含学生答案正文")
			}
			continue
		}
		if !strings.Contains(body, want) {
			t.Fatalf("教师汇总缺少 %q", want)
		}
	}

	code, body = getPage(t, assistant, env.ts.URL+"/assignments/"+id+"/summary")
	if code != http.StatusOK || !strings.Contains(body, "只读统计") {
		t.Fatalf("助教汇总应为只读统计，实际 %d", code)
	}
	if strings.Contains(body, "学生最终状态") || strings.Contains(body, "奖励流水（合计") || strings.Contains(body, "时间线（首次提交") {
		t.Fatal("助教汇总不应出现明细、流水或时间线")
	}

	code, body = getPage(t, student, env.ts.URL+"/assignments/"+id+"/summary")
	if code != http.StatusForbidden || !strings.Contains(body, "forbidden_role") {
		t.Fatalf("学生汇总应 403，实际 %d", code)
	}
}

func TestAssignmentsPageEditDraft(t *testing.T) {
	env := newTestEnv(t)
	teacher := env.login(t, "T01", store.DemoPassword)

	code, body := getPage(t, teacher, env.ts.URL+"/assignments?edit=HW4")
	if code != http.StatusOK || !strings.Contains(body, "编辑草稿") || !strings.Contains(body, "草稿作业") {
		t.Fatalf("编辑草稿应预填表单，实际 %d", code)
	}
	if !strings.Contains(body, `action="/assignments/HW4"`) {
		t.Fatal("编辑表单应提交到更新地址")
	}

	code, out := postPage(t, teacher, env.ts.URL+"/assignments/HW4", url.Values{
		"title":       {"草稿改过的标题"},
		"requirement": {"新的要求"},
		"deadline_at": {time.Now().Add(72 * time.Hour).Format("2006-01-02T15:04")},
	})
	if code != http.StatusSeeOther || !strings.Contains(out, "ok=updated") {
		t.Fatalf("保存草稿应 303，实际 %d %s", code, out)
	}
	code, body = getPage(t, teacher, env.ts.URL+"/assignments")
	if code != http.StatusOK || !strings.Contains(body, "草稿改过的标题") {
		t.Fatalf("列表应显示更新后的标题，实际 %d", code)
	}

	// 已发布作业不可编辑：页面必须给出可读原因
	code, body = getPage(t, teacher, env.ts.URL+"/assignments?edit=HW1")
	if code != http.StatusConflict || !strings.Contains(body, "assignment_locked") {
		t.Fatalf("编辑已发布作业应 409 页面，实际 %d", code)
	}
}

func TestPageRoleRouting(t *testing.T) {
	env := newTestEnv(t)
	teacher := env.login(t, "T01", store.DemoPassword)
	assistant := env.login(t, "A01", store.DemoPassword)
	student := env.login(t, "S01", store.DemoPassword)

	if code, body := getPage(t, teacher, env.ts.URL+"/"); code != http.StatusSeeOther || !strings.Contains(body, "/assignments") {
		t.Fatalf("登录后访问 / 应跳作业列表，实际 %d", code)
	}
	if code, body := getPage(t, teacher, env.ts.URL+"/assignments/HW1"); code != http.StatusSeeOther || !strings.Contains(body, "/review") {
		t.Fatalf("教师访问学生页应跳批阅台，实际 %d", code)
	}
	if code, body := getPage(t, assistant, env.ts.URL+"/assignments/HW1"); code != http.StatusSeeOther || !strings.Contains(body, "/board") {
		t.Fatalf("助教访问学生页应跳看板，实际 %d", code)
	}

	// 草稿对非教师不可见：列表里不应出现
	for _, c := range []*http.Client{assistant, student} {
		code, body := getPage(t, c, env.ts.URL+"/assignments")
		if code != http.StatusOK {
			t.Fatalf("作业列表应 200，实际 %d", code)
		}
		if strings.Contains(body, "草稿作业") {
			t.Fatal("草稿不应出现在学生/助教列表中")
		}
	}
}

func TestStudentPageAfterPassed(t *testing.T) {
	env := newTestEnv(t)
	teacher := env.login(t, "T01", store.DemoPassword)
	student := env.login(t, "S01", store.DemoPassword)
	id := publishAssignment(t, env, teacher)
	sub := submitOne(t, env, student, id, "通过的答案", "page-req-p", 0)
	if code, _ := doJSONBody(t, teacher, http.MethodPost, env.ts.URL+"/api/assignments/"+id+"/reviews",
		jsonBody(t, reviewBody(versionID(t, sub), store.ReviewPass, "很好"))); code != http.StatusCreated {
		t.Fatalf("批阅失败: %d", code)
	}

	code, body := getPage(t, student, env.ts.URL+"/assignments/"+id)
	if code != http.StatusOK || !strings.Contains(body, "已通过") || !strings.Contains(body, "无需再提交") {
		t.Fatalf("通过后应提示无需提交，实际 %d", code)
	}
	if strings.Contains(body, `name="content"`) {
		t.Fatal("通过后不应再显示提交表单")
	}
	if !strings.Contains(body, "首次通过") || !strings.Contains(body, "累计") {
		t.Fatal("通过后应显示积分记录")
	}
}

func TestPageFormValidationErrors(t *testing.T) {
	env := newTestEnv(t)
	teacher := env.login(t, "T01", store.DemoPassword)
	assistant := env.login(t, "A01", store.DemoPassword)
	student := env.login(t, "S01", store.DemoPassword)
	id := publishAssignment(t, env, teacher)

	// 截止时间格式错误 → 400 invalid_input
	code, body := postPage(t, teacher, env.ts.URL+"/assignments", url.Values{
		"title": {"x"}, "requirement": {"y"}, "deadline_at": {"不是时间"},
	})
	if code != http.StatusBadRequest || !strings.Contains(body, "invalid_input") {
		t.Fatalf("错误截止时间应 400 页面，实际 %d", code)
	}

	// 补交原因过短 → 400 invalid_input
	code, body = postPage(t, teacher, env.ts.URL+"/assignments/"+id+"/extension", url.Values{
		"student_id": {"S02"}, "deadline_at": {time.Now().Add(96 * time.Hour).Format("2006-01-02T15:04")},
		"reason": {"太短"},
	})
	if code != http.StatusBadRequest || !strings.Contains(body, "invalid_input") {
		t.Fatalf("原因过短应 400 页面，实际 %d", code)
	}

	// 催交方式非法 → 400 invalid_input
	code, body = postPage(t, assistant, env.ts.URL+"/assignments/"+id+"/reminder", url.Values{
		"student_id": {"S02"}, "channel": {"wechat"}, "note": {"提醒"},
	})
	if code != http.StatusBadRequest || !strings.Contains(body, "invalid_input") {
		t.Fatalf("非法催交方式应 400 页面，实际 %d", code)
	}

	// 提交 base_version 非整数 → 400
	code, body = postPage(t, student, env.ts.URL+"/assignments/"+id+"/submit", url.Values{
		"content": {"答案"}, "request_id": {"page-bad"}, "base_version": {"abc"},
	})
	if code != http.StatusBadRequest || !strings.Contains(body, "invalid_input") {
		t.Fatalf("非法 base_version 应 400 页面，实际 %d", code)
	}
}

func TestPageRejectsCrossClass(t *testing.T) {
	env := newTestEnv(t)
	outsider := env.login(t, "X01", store.DemoPassword)

	code, body := getPage(t, outsider, env.ts.URL+"/assignments/HW1")
	if code != http.StatusNotFound || !strings.Contains(body, "not_found") {
		t.Fatalf("外班访问作业页应 404 页面，实际 %d %s", code, body)
	}
}
