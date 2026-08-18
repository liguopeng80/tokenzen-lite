// Package secrets 提供渠道上游密钥的对称加密存储（AES-256-GCM）。
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

// Box 持有加密密钥。
type Box struct{ key [32]byte }

// New 从任意长度的密钥材料派生 256 位密钥。
func New(material string) *Box {
	b := &Box{}
	b.key = sha256.Sum256([]byte(material))
	return b
}

// Encrypt 加密明文，输出 base64(nonce || ciphertext)。
func (b *Box) Encrypt(plain string) (string, error) {
	block, err := aes.NewCipher(b.key[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := gcm.Seal(nonce, nonce, []byte(plain), nil)
	return base64.StdEncoding.EncodeToString(out), nil
}

// Decrypt 解密 Encrypt 的输出。
func (b *Box) Decrypt(encoded string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("密文格式错误: %w", err)
	}
	block, err := aes.NewCipher(b.key[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", fmt.Errorf("密文长度不足")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if err != nil {
		return "", fmt.Errorf("解密失败（加密密钥是否变更？）: %w", err)
	}
	return string(plain), nil
}
