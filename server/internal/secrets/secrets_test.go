package secrets

import (
	"encoding/base64"
	"strings"
	"testing"
)

// TestNewEmptyMaterial 空密钥材料的行为。
// 说明：当前实现 New("") 不报错——它返回 key=SHA-256("") 的 Box，
// 即一个公开常量密钥。生产环境由 config 层（config.go:116）拦截空 EncryptKey，
// 因此应用路径不会触发。这里以测试钉住「现状不报错」的事实，
// 一旦未来在 New 内增加空材料校验，本测试需同步改为期望 error。
func TestNewEmptyMaterial(t *testing.T) {
	box := New("")
	if box == nil {
		t.Fatal("New(\"\") 返回 nil，预期返回非空 Box")
	}
	// 现状：空材料派生的 Box 仍可加解密往返。
	plain := "dangerous-default-key"
	enc, err := box.Encrypt(plain)
	if err != nil {
		t.Fatalf("空材料 Box.Encrypt 失败: %v", err)
	}
	got, err := box.Decrypt(enc)
	if err != nil {
		t.Fatalf("空材料 Box.Decrypt 失败: %v", err)
	}
	if got != plain {
		t.Fatalf("空材料往返不一致: got %q want %q", got, plain)
	}
}

// TestEncryptDecryptRoundTrip 正常往返覆盖典型明文。
// 业务后果：渠道上游密钥加密后无法还原 = 密钥永久丢失，渠道不可用。
func TestEncryptDecryptRoundTrip(t *testing.T) {
	box := New("some-strong-material-12345")
	cases := []struct {
		name  string
		plain string
	}{
		{"典型文本", "sk-upstream-abcdef0123456789"},
		{"空明文", ""},
		{"中文与符号", "密钥材料！@#$%^&*()"},
		{"长文本", strings.Repeat("abcdefgh", 512)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			enc, err := box.Encrypt(c.plain)
			if err != nil {
				t.Fatalf("Encrypt 失败: %v", err)
			}
			got, err := box.Decrypt(enc)
			if err != nil {
				t.Fatalf("Decrypt 失败: %v", err)
			}
			if got != c.plain {
				t.Fatalf("往返不一致: got %q want %q", got, c.plain)
			}
		})
	}
}

// TestDecryptWrongKeyFails 用密钥 B 解密密钥 A 的密文必须失败。
// GCM 认证标签校验不通过 → "解密失败"。
func TestDecryptWrongKeyFails(t *testing.T) {
	enc, err := New("key-A").Encrypt("secret")
	if err != nil {
		t.Fatalf("Encrypt 失败: %v", err)
	}
	if _, err := New("key-B").Decrypt(enc); err == nil {
		t.Fatal("错误密钥解密应失败，实际成功")
	}
}

// TestDecryptCorruptCiphertextFails 篡改密文字节必须失败。
func TestDecryptCorruptCiphertextFails(t *testing.T) {
	box := New("material")
	enc, err := box.Encrypt("plain")
	if err != nil {
		t.Fatalf("Encrypt 失败: %v", err)
	}
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		t.Fatalf("base64 解码失败: %v", err)
	}
	// 翻转密文区末尾字节（GCM 标签区），触发认证失败。
	raw[len(raw)-1] ^= 0xFF
	corrupted := base64.StdEncoding.EncodeToString(raw)
	if _, err := box.Decrypt(corrupted); err == nil {
		t.Fatal("损坏密文解密应失败，实际成功")
	}
}

// TestDecryptTruncatedCiphertextFails 长度不足 nonce 的密文必须报「密文长度不足」。
func TestDecryptTruncatedCiphertextFails(t *testing.T) {
	box := New("material")
	// GCM 标准 nonce 长度为 12 字节；这里给 5 字节（< 12）。
	short := base64.StdEncoding.EncodeToString([]byte("12345"))
	_, err := box.Decrypt(short)
	if err == nil {
		t.Fatal("截断密文解密应失败，实际成功")
	}
	if !strings.Contains(err.Error(), "密文长度不足") {
		t.Fatalf("截断密文应报「密文长度不足」，得到: %v", err)
	}
}

// TestDecryptInvalidBase64Fails 非 base64 输入必须报「密文格式错误」。
func TestDecryptInvalidBase64Fails(t *testing.T) {
	box := New("material")
	_, err := box.Decrypt("!!!not-base64!!!")
	if err == nil {
		t.Fatal("非法 base64 解密应失败，实际成功")
	}
	if !strings.Contains(err.Error(), "密文格式错误") {
		t.Fatalf("非法 base64 应报「密文格式错误」，得到: %v", err)
	}
}

// TestEncryptUniqueNonce 相同明文每次加密产生不同密文（随机 nonce），
// 且每条密文都能独立解密回原文。
func TestEncryptUniqueNonce(t *testing.T) {
	box := New("material")
	plain := "same-plaintext"
	a, err := box.Encrypt(plain)
	if err != nil {
		t.Fatalf("首次 Encrypt 失败: %v", err)
	}
	b, err := box.Encrypt(plain)
	if err != nil {
		t.Fatalf("二次 Encrypt 失败: %v", err)
	}
	if a == b {
		t.Fatal("相同明文两次加密产生相同密文，nonce 可能未随机化")
	}
	for i, enc := range []string{a, b} {
		got, err := box.Decrypt(enc)
		if err != nil {
			t.Fatalf("第 %d 条密文解密失败: %v", i+1, err)
		}
		if got != plain {
			t.Fatalf("第 %d 条密文往返不一致: got %q want %q", i+1, got, plain)
		}
	}
}
