// Package auth 提供口令哈希与校验。
//
// 采用 PBKDF2-HMAC-SHA256（标准库实现，无第三方依赖）。教学演示账号口令统一，
// 但存储形式与生产一致：只存盐与派生值，不存明文。
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	iterations = 120000
	saltLen    = 16
	keyLen     = 32
	scheme     = "pbkdf2-sha256"
)

// ErrInvalidHash 表示存储的哈希格式非法。
var ErrInvalidHash = errors.New("口令哈希格式非法")

// HashPassword 生成随机盐并返回可存库的编码串：pbkdf2-sha256$iter$salt$key。
func HashPassword(password string) (string, error) {
	if password == "" {
		return "", errors.New("口令不能为空")
	}
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("生成盐失败: %w", err)
	}
	key := pbkdf2SHA256([]byte(password), salt, iterations, keyLen)
	return fmt.Sprintf("%s$%d$%s$%s", scheme, iterations,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword 校验明文口令与编码串是否匹配；使用常量时间比较。
func VerifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != scheme {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter <= 0 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil || len(want) == 0 {
		return false
	}
	got := pbkdf2SHA256([]byte(password), salt, iter, len(want))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// pbkdf2SHA256 是 PBKDF2（RFC 8018）在 HMAC-SHA256 上的最小实现。
func pbkdf2SHA256(password, salt []byte, iter, keyLen int) []byte {
	prf := hmac.New(sha256.New, password)
	hashLen := prf.Size()
	blocks := (keyLen + hashLen - 1) / hashLen
	out := make([]byte, 0, blocks*hashLen)

	buf := make([]byte, 4)
	for block := 1; block <= blocks; block++ {
		prf.Reset()
		prf.Write(salt)
		buf[0] = byte(block >> 24)
		buf[1] = byte(block >> 16)
		buf[2] = byte(block >> 8)
		buf[3] = byte(block)
		prf.Write(buf)

		u := prf.Sum(nil)
		t := make([]byte, len(u))
		copy(t, u)

		for i := 1; i < iter; i++ {
			prf.Reset()
			prf.Write(u)
			u = prf.Sum(u[:0])
			for j := range t {
				t[j] ^= u[j]
			}
		}
		out = append(out, t...)
	}
	return out[:keyLen]
}
