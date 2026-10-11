package auth

import (
	"testing"
	"time"
)

func TestSessionIssueVerify(t *testing.T) {
	m := NewManager([]byte("0123456789abcdef0123456789abcdef"))
	tok := m.Issue(ScopeAdmin, time.Hour)

	scope, ok := m.Verify(tok)
	if !ok || scope != ScopeAdmin {
		t.Fatalf("Verify = (%q, %v), want admin/true", scope, ok)
	}
	// 篡改
	if _, ok := m.Verify(tok + "x"); ok {
		t.Fatal("篡改令牌不应通过")
	}
	// 过期
	expired := NewManager([]byte("0123456789abcdef0123456789abcdef")).Issue(ScopeView, -time.Minute)
	if _, ok := m.Verify(expired); ok {
		t.Fatal("过期令牌不应通过")
	}
	// 其他密钥
	other := NewManager([]byte("ffffffffffffffffffffffffffffffff")).Issue(ScopeAdmin, time.Hour)
	if _, ok := m.Verify(other); ok {
		t.Fatal("异钥令牌不应通过")
	}
}

func TestAllows(t *testing.T) {
	if !Allows(ScopeAdmin, ScopeView) {
		t.Error("admin 应满足 view")
	}
	if Allows(ScopeView, ScopeAdmin) {
		t.Error("view 不应满足 admin")
	}
}

func TestPassword(t *testing.T) {
	h, err := HashPassword("secret123")
	if err != nil {
		t.Fatal(err)
	}
	if !CheckPassword(h, "secret123") || CheckPassword(h, "wrong") {
		t.Fatal("密码校验逻辑错误")
	}
}
