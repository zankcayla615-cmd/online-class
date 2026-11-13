package httpapi_test

import (
	"net/http"
	"testing"
	"time"

	"onlineclass/internal/store"
)

func extensionBody(studentID, deadlineAt, reason string) map[string]any {
	return map[string]any{"student_id": studentID, "deadline_at": deadlineAt, "reason": reason}
}

func TestGrantExtensionOverHTTP(t *testing.T) {
	env := newTestEnv(t)
	teacher := env.login(t, "T01", store.DemoPassword)
	student := env.login(t, "S01", store.DemoPassword)

	seedPastDeadlineAssignment(t, env, "HW-PAST")
	path := env.ts.URL + "/api/assignments/HW-PAST/extensions"
	deadline := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)

	code, granted := doJSONBody(t, teacher, http.MethodPost, path,
		jsonBody(t, extensionBody("S01", deadline, "生病住院，需要补交")))
	if code != http.StatusCreated {
		t.Fatalf("批准补交应 201，实际 %d %v", code, granted)
	}
	if granted["student_id"] != "S01" || granted["granted_by"] != "T01" || granted["reason"] != "生病住院，需要补交" {
		t.Fatalf("补交响应不符: %v", granted)
	}

	// 被授权学生可提交，且标记补交生效
	code, submitted := doJSONBody(t, student, http.MethodPost,
		env.ts.URL+"/api/assignments/HW-PAST/submissions", jsonBody(t, submitBody("补交作业", "req-ext", 0)))
	if code != http.StatusCreated || submitted["extension_applied"] != true {
		t.Fatalf("补交后应可提交且 extension_applied=true，实际 %d %v", code, submitted)
	}

	// 其他学生仍逾期
	other := env.login(t, "S02", store.DemoPassword)
	code, rejected := doJSONBody(t, other, http.MethodPost,
		env.ts.URL+"/api/assignments/HW-PAST/submissions", jsonBody(t, submitBody("迟交", "req-late", 0)))
	if code != http.StatusConflict || rejected["code"] != "deadline_passed" {
		t.Fatalf("未授权学生应仍逾期，实际 %d %v", code, rejected)
	}

	// 只能设置一次
	code, again := doJSONBody(t, teacher, http.MethodPost, path,
		jsonBody(t, extensionBody("S01", deadline, "再次申请补交")))
	if code != http.StatusConflict || again["code"] != "extension_exists" {
		t.Fatalf("重复设置应 409 extension_exists，实际 %d %v", code, again)
	}

	// 补交截止必须晚于原截止
	early := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339)
	code, invalid := doJSONBody(t, teacher, http.MethodPost, path,
		jsonBody(t, extensionBody("S03", early, "生病住院，需要补交")))
	if code != http.StatusBadRequest || invalid["code"] != "extension_deadline_invalid" {
		t.Fatalf("补交截止不晚于原截止应 400，实际 %d %v", code, invalid)
	}
}

func TestGrantExtensionPermissionsAndValidationOverHTTP(t *testing.T) {
	env := newTestEnv(t)
	teacher := env.login(t, "T01", store.DemoPassword)
	assistant := env.login(t, "A01", store.DemoPassword)
	student := env.login(t, "S01", store.DemoPassword)

	seedPastDeadlineAssignment(t, env, "HW-PAST")
	path := env.ts.URL + "/api/assignments/HW-PAST/extensions"
	deadline := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)

	if code, _ := doJSONBody(t, assistant, http.MethodPost, path, jsonBody(t, extensionBody("S01", deadline, "生病住院，需要补交"))); code != http.StatusForbidden {
		t.Fatalf("助教补交应 403，实际 %d", code)
	}
	if code, _ := doJSONBody(t, student, http.MethodPost, path, jsonBody(t, extensionBody("S01", deadline, "生病住院，需要补交"))); code != http.StatusForbidden {
		t.Fatalf("学生补交应 403，实际 %d", code)
	}
	if code, _ := doJSONBody(t, teacher, http.MethodPost, env.ts.URL+"/api/assignments/HW2/extensions",
		jsonBody(t, extensionBody("S01", deadline, "生病住院，需要补交"))); code != http.StatusNotFound {
		t.Fatalf("跨班补交应 404，实际 %d", code)
	}
	if code, _ := doJSONBody(t, teacher, http.MethodPost, path, jsonBody(t, extensionBody("S01", deadline, "生病"))); code != http.StatusBadRequest {
		t.Fatalf("原因过短应 400，实际 %d", code)
	}
	if code, _ := doJSONBody(t, teacher, http.MethodPost, path, jsonBody(t, extensionBody("X01", deadline, "生病住院，需要补交"))); code != http.StatusNotFound {
		t.Fatalf("外班学生应 404，实际 %d", code)
	}
	if code, _ := doJSONBody(t, teacher, http.MethodPost, path, jsonBody(t, map[string]any{
		"student_id": "S01", "deadline_at": "不是时间", "reason": "生病住院，需要补交",
	})); code != http.StatusBadRequest {
		t.Fatalf("非法时间应 400，实际 %d", code)
	}
	if code, _ := doJSONBody(t, teacher, http.MethodPost, path, jsonBody(t, map[string]any{
		"student_id": "S01", "deadline_at": deadline, "reason": "生病住院，需要补交", "extra": 1,
	})); code != http.StatusBadRequest {
		t.Fatalf("未知字段应 400，实际 %d", code)
	}
	if code, _ := doJSONBody(t, teacher, http.MethodPost, path, jsonBody(t, extensionBody("S01", deadline, "生病住院，需要补交"))); code != http.StatusCreated {
		t.Fatalf("合法补交应 201，实际 %d", code)
	}
}
