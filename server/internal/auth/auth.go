// 用户侧认证：密码哈希（bcrypt）与签名会话 Cookie（HMAC-SHA256，无状态）。
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// 会话作用域：admin > view
const (
	ScopeAdmin = "admin"
	ScopeView  = "view"
)

// CookieName 会话 Cookie 名。
const CookieName = "cc_session"

// HashPassword bcrypt 哈希密码。
func HashPassword(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	return string(b), err
}

// CheckPassword 校验密码哈希。
func CheckPassword(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

// RandomHex 生成随机十六进制串（join key 用）。
func RandomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Manager 基于主密钥签发/校验会话令牌。
type Manager struct {
	key []byte
}

// NewManager 创建会话管理器（复用 AES 主密钥）。
func NewManager(key []byte) *Manager {
	return &Manager{key: key}
}

// Issue 签发 scope 会话令牌，ttl 内有效。
func (m *Manager) Issue(scope string, ttl time.Duration) string {
	exp := time.Now().Add(ttl).Unix()
	payload := scope + "|" + strconv.FormatInt(exp, 10)
	sig := m.sign(payload)
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + sig
}

// Verify 校验令牌并返回 (scope, true)；无效返回 ("", false)。
func (m *Manager) Verify(token string) (string, bool) {
	if token == "" || !strings.Contains(token, ".") {
		return "", false
	}
	i := strings.LastIndexByte(token, '.')
	raw, err := base64.RawURLEncoding.DecodeString(token[:i])
	if err != nil {
		return "", false
	}
	payload := string(raw)
	if !hmac.Equal([]byte(m.sign(payload)), []byte(token[i+1:])) {
		return "", false
	}
	parts := strings.Split(payload, "|")
	if len(parts) != 2 {
		return "", false
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return "", false
	}
	switch parts[0] {
	case ScopeAdmin, ScopeView:
		return parts[0], true
	default:
		return "", false
	}
}

// Allows 判断实际 scope 是否满足所需权限（admin 涵盖 view）。
func Allows(actual, need string) bool {
	if need == ScopeView {
		return actual == ScopeView || actual == ScopeAdmin
	}
	return actual == need
}

// ---------- 机器 API Key（Bearer 通道，供 App / 脚本等非浏览器客户端）----------

// APIKeyPrefix API Key 固定前缀：便于在日志与配置中识别凭证类型。
const APIKeyPrefix = "cck_"

// NewAPIKey 生成 API Key：cck_ + 32 字节随机数的十六进制。
func NewAPIKey() string {
	return APIKeyPrefix + RandomHex(32)
}

// HashAPIKey API Key 的存储形式（SHA-256 十六进制）。
// Key 本身是高熵随机串，无需 bcrypt 慢哈希；且每请求都要校验，须保持廉价。
func HashAPIKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// CheckAPIKey 校验明文 Key 与存储哈希是否匹配（常量时间比较，避免计时侧信道）。
func CheckAPIKey(storedHash, provided string) bool {
	if storedHash == "" || provided == "" {
		return false
	}
	return hmac.Equal([]byte(HashAPIKey(provided)), []byte(storedHash))
}

func (m *Manager) sign(payload string) string {
	h := hmac.New(sha256.New, m.key)
	h.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

// ErrWeakPassword 密码强度不足。
var ErrWeakPassword = errors.New("密码至少 6 位")
