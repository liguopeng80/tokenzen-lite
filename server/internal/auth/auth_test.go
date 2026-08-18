package auth

import (
	"strings"
	"testing"
)

func TestHashAndVerifyPassword(t *testing.T) {
	hash, err := HashPassword("correct-horse")
	if err != nil {
		t.Fatalf("哈希失败: %v", err)
	}
	if hash == "correct-horse" {
		t.Fatal("哈希不应等于明文")
	}
	if !VerifyPassword(hash, "correct-horse") {
		t.Error("正确密码应通过校验")
	}
	if VerifyPassword(hash, "wrong-password") {
		t.Error("错误密码不应通过校验")
	}
}

func TestHashPasswordTooShort(t *testing.T) {
	if _, err := HashPassword("short"); err == nil {
		t.Error("短于 8 位的密码应报错")
	}
}

func TestGenerateKey(t *testing.T) {
	k1, err := GenerateKey()
	if err != nil {
		t.Fatalf("生成失败: %v", err)
	}
	if !strings.HasPrefix(k1.Plain, KeyPlainPrefix) {
		t.Errorf("明文应有 %s 前缀: %s", KeyPlainPrefix, k1.Plain)
	}
	if !strings.HasPrefix(k1.Plain, k1.Prefix) {
		t.Errorf("Prefix 应是明文前缀: %s / %s", k1.Prefix, k1.Plain)
	}
	if k1.Hash != HashKey(k1.Plain) {
		t.Error("Hash 应与 HashKey(明文) 一致")
	}
	if !LooksLikeKey(k1.Plain) {
		t.Error("生成的 Key 应通过 LooksLikeKey")
	}

	k2, _ := GenerateKey()
	if k1.Plain == k2.Plain {
		t.Error("两次生成的 Key 不应相同")
	}
}
