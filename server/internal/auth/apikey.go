package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
)

// KeyPlainPrefix 是全部 API Key 明文的固定前缀。
const KeyPlainPrefix = "tzl-"

// ServiceTokenPlainPrefix 是接入方服务令牌明文的固定前缀，与用户 API Key 区分，
// 使日志与列表肉眼可辨，且 LooksLikeKey 不会把服务令牌误判为用户 Key。
const ServiceTokenPlainPrefix = "tzs-"

// GeneratedKey 是一次 API Key 生成的结果。明文仅在创建响应中返回一次。
type GeneratedKey struct {
	Plain  string // 完整明文，形如 tzl-xxxxx
	Hash   string // SHA-256 十六进制，落库
	Prefix string // 明文前缀片段（tzl- + 8 位），供列表展示识别
}

// GenerateKey 生成新的 API Key。
func GenerateKey() (*GeneratedKey, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("生成随机密钥失败: %w", err)
	}
	body := base64.RawURLEncoding.EncodeToString(raw)
	plain := KeyPlainPrefix + body
	return &GeneratedKey{
		Plain:  plain,
		Hash:   HashKey(plain),
		Prefix: plain[:len(KeyPlainPrefix)+8],
	}, nil
}

// HashKey 计算 API Key 明文的落库哈希。
func HashKey(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

// LooksLikeKey 快速判断字符串是否可能是本系统签发的 Key。
func LooksLikeKey(s string) bool {
	return strings.HasPrefix(s, KeyPlainPrefix) && len(s) > len(KeyPlainPrefix)+16
}

// LooksLikeServiceToken 快速判断字符串是否可能是接入方服务令牌。
func LooksLikeServiceToken(s string) bool {
	return strings.HasPrefix(s, ServiceTokenPlainPrefix) && len(s) > len(ServiceTokenPlainPrefix)+16
}

// GenerateServiceToken 生成接入方服务令牌。明文仅在创建响应中返回一次，落库的是
// HashKey（前缀无关，复用同一哈希算法）。形态与 GenerateKey 一致，仅前缀不同。
func GenerateServiceToken() (*GeneratedKey, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("生成服务令牌失败: %w", err)
	}
	body := base64.RawURLEncoding.EncodeToString(raw)
	plain := ServiceTokenPlainPrefix + body
	return &GeneratedKey{
		Plain:  plain,
		Hash:   HashKey(plain),
		Prefix: plain[:len(ServiceTokenPlainPrefix)+8],
	}, nil
}
