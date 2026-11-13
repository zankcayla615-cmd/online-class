package apperr_test

import (
	"errors"
	"testing"

	"onlineclass/internal/apperr"
)

func TestConstructors(t *testing.T) {
	cases := []struct {
		err    *apperr.Error
		status int
		code   string
	}{
		{apperr.Forbidden("无权"), 403, "forbidden_role"},
		{apperr.NotFound("不存在"), 404, "not_found"},
		{apperr.Invalid("参数错"), 400, "invalid_input"},
		{apperr.Conflict("stale_version", "版本过期"), 409, "stale_version"},
	}
	for _, c := range cases {
		if c.err.Status != c.status || c.err.Code != c.code {
			t.Fatalf("构造错误不符: %+v", c.err)
		}
		var target *apperr.Error
		if !errors.As(error(c.err), &target) {
			t.Fatal("应可被 errors.As 识别")
		}
	}
}

func TestWithDetails(t *testing.T) {
	e := apperr.Conflict("stale_version", "版本过期").WithDetails(map[string]any{"version_id": "v1"})
	e.WithDetails(map[string]any{"current_version_no": 2})
	if e.Details["version_id"] != "v1" || e.Details["current_version_no"] != 2 {
		t.Fatalf("明细未合并: %+v", e.Details)
	}
	if e.Error() == "" {
		t.Fatal("Error() 不应为空")
	}
}

func TestErrorWithoutDetails(t *testing.T) {
	e := apperr.Invalid("参数错误")
	if e.Error() != "invalid_input: 参数错误" {
		t.Fatalf("Error() 输出不符: %s", e.Error())
	}
}
