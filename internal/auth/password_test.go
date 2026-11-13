package auth_test

import (
	"strings"
	"testing"

	"onlineclass/internal/auth"
)

func TestHashAndVerify(t *testing.T) {
	encoded, err := auth.HashPassword("demo1234")
	if err != nil {
		t.Fatalf("生成哈希失败: %v", err)
	}
	if strings.Contains(encoded, "demo1234") {
		t.Fatal("哈希串不得包含明文口令")
	}
	if !auth.VerifyPassword(encoded, "demo1234") {
		t.Fatal("正确口令应校验通过")
	}
	if auth.VerifyPassword(encoded, "wrong-password") {
		t.Fatal("错误口令不应通过")
	}
}

func TestHashUsesRandomSalt(t *testing.T) {
	a, err := auth.HashPassword("same")
	if err != nil {
		t.Fatalf("生成哈希失败: %v", err)
	}
	b, err := auth.HashPassword("same")
	if err != nil {
		t.Fatalf("生成哈希失败: %v", err)
	}
	if a == b {
		t.Fatal("相同口令两次哈希应因随机盐而不同")
	}
}

func TestVerifyRejectsMalformed(t *testing.T) {
	cases := []string{
		"",
		"plain",
		"pbkdf2-sha256$0$AAA$BBB",
		"pbkdf2-sha256$120000$!!!$BBB",
		"pbkdf2-sha256$120000$AAA$!!!",
		"md5$1$AAA$BBB",
	}
	for _, c := range cases {
		if auth.VerifyPassword(c, "demo1234") {
			t.Fatalf("非法哈希应被拒绝: %q", c)
		}
	}
}

func TestHashEmptyPassword(t *testing.T) {
	if _, err := auth.HashPassword(""); err == nil {
		t.Fatal("空口令应报错")
	}
}
