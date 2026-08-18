package auth

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

// initialPasswordBytes 生成初始密码的随机字节数。12 字节经 base64 编码为 16 个字符，
// 强度远高于管理员手工拟定的密码，且短到可以口头或即时消息转达一次。
const initialPasswordBytes = 12

// GenerateInitialPassword 生成一次性初始密码。明文只在建号响应中返回一次，
// 不落库、不进日志与审计；账号随之带上「首次登录必须改密」标志。
func GenerateInitialPassword() (string, error) {
	raw := make([]byte, initialPasswordBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("生成初始密码失败: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// HashPassword 生成密码的 bcrypt 哈希。
func HashPassword(plain string) (string, error) {
	if len(plain) < 8 {
		return "", fmt.Errorf("密码长度不能少于 8 位")
	}
	b, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("密码哈希失败: %w", err)
	}
	return string(b), nil
}

// VerifyPassword 校验明文密码与哈希是否匹配。
func VerifyPassword(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}
