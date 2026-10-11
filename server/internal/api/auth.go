// 用户侧认证与授权：初始化/登录/会话/密码与视图密码管理、中间件。
package api

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"cc_dashboard/server/internal/auth"
)

// settings 键名
const (
	setAdminHash   = "admin_password_hash"
	setViewEnabled = "view_password_enabled"
	setViewHash    = "view_password_hash"
	setJoinKey     = "join_key"
)

const sessionTTL = 7 * 24 * time.Hour

// ---------- 中间件 ----------

func (s *Server) sessionScope(c *gin.Context) string {
	tok, err := c.Cookie(auth.CookieName)
	if err != nil {
		return ""
	}
	scope, ok := s.authm.Verify(tok)
	if !ok {
		return ""
	}
	return scope
}

func (s *Server) isAdmin(c *gin.Context) bool {
	return s.sessionScope(c) == auth.ScopeAdmin
}

func (s *Server) viewEnabled() bool {
	v, _, err := s.db.GetSetting(setViewEnabled)
	return err == nil && v == "1"
}

// requireAdmin 后台写操作：必须 admin 会话。
func (s *Server) requireAdmin(c *gin.Context) {
	if !s.isAdmin(c) {
		fail(c, http.StatusUnauthorized, "需要管理员登录")
		c.Abort() // 必须中止，否则后续 handler 仍会执行
		return
	}
	c.Next()
}

// requireView 前台读操作：开启访问密码时需 view/admin 会话，未开启放行。
func (s *Server) requireView(c *gin.Context) {
	if !s.viewEnabled() {
		c.Next()
		return
	}
	scope := s.sessionScope(c)
	if scope == "" || !auth.Allows(scope, auth.ScopeView) {
		fail(c, http.StatusUnauthorized, "需要访问密码")
		c.Abort()
		return
	}
	c.Next()
}

// ---------- 公开接口 ----------

// GET /api/v1/auth/status
func (s *Server) handleAuthStatus(c *gin.Context) {
	hash, _, _ := s.db.GetSetting(setAdminHash)
	ve, _, _ := s.db.GetSetting(setViewEnabled)
	c.JSON(http.StatusOK, gin.H{
		"admin_set":    hash != "",
		"view_enabled": ve == "1",
	})
}

// GET /api/v1/auth/whoami
func (s *Server) handleWhoami(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"scope": s.sessionScope(c)})
}

// POST /api/v1/auth/setup — 首次设置管理员密码（仅一次）
func (s *Server) handleSetup(c *gin.Context) {
	hash, _, _ := s.db.GetSetting(setAdminHash)
	if hash != "" {
		fail(c, http.StatusBadRequest, "管理员密码已设置")
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || len(body.Password) < 6 {
		fail(c, http.StatusBadRequest, auth.ErrWeakPassword.Error())
		return
	}
	h, err := auth.HashPassword(body.Password)
	if err != nil {
		fail(c, http.StatusInternalServerError, "hash failed")
		return
	}
	if err := s.db.SetSetting(setAdminHash, h); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	s.setSession(c, auth.ScopeAdmin)
	c.JSON(http.StatusOK, gin.H{"ok": true, "scope": auth.ScopeAdmin})
}

// POST /api/v1/auth/login {password, scope: admin|view}
func (s *Server) handleLogin(c *gin.Context) {
	var body struct {
		Password string `json:"password"`
		Scope    string `json:"scope"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		fail(c, http.StatusBadRequest, "invalid json")
		return
	}
	adminHash, _, _ := s.db.GetSetting(setAdminHash)

	switch body.Scope {
	case auth.ScopeView:
		ve, _, _ := s.db.GetSetting(setViewEnabled)
		if ve != "1" {
			// 前台未开密码：直接给 view 会话
			s.setSession(c, auth.ScopeView)
			c.JSON(http.StatusOK, gin.H{"ok": true, "scope": auth.ScopeView})
			return
		}
		vh, _, _ := s.db.GetSetting(setViewHash)
		target := vh
		if target == "" {
			target = adminHash // 未单独设置时用管理员密码
		}
		if target == "" || !auth.CheckPassword(target, body.Password) {
			fail(c, http.StatusUnauthorized, "密码错误")
			return
		}
		s.setSession(c, auth.ScopeView)
		c.JSON(http.StatusOK, gin.H{"ok": true, "scope": auth.ScopeView})
	default: // admin
		if adminHash == "" {
			fail(c, http.StatusBadRequest, "请先初始化管理员密码")
			return
		}
		if !auth.CheckPassword(adminHash, body.Password) {
			fail(c, http.StatusUnauthorized, "密码错误")
			return
		}
		s.setSession(c, auth.ScopeAdmin)
		c.JSON(http.StatusOK, gin.H{"ok": true, "scope": auth.ScopeAdmin})
	}
}

// POST /api/v1/auth/logout
func (s *Server) handleLogout(c *gin.Context) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(auth.CookieName, "", -1, "/", "", false, true)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ---------- 管理接口（requireAdmin 已挂载） ----------

// POST /api/v1/auth/password {old_password, new_password}
func (s *Server) handleChangePassword(c *gin.Context) {
	var body struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		fail(c, http.StatusBadRequest, "invalid json")
		return
	}
	hash, _, _ := s.db.GetSetting(setAdminHash)
	if hash == "" || !auth.CheckPassword(hash, body.OldPassword) {
		fail(c, http.StatusUnauthorized, "原密码错误")
		return
	}
	if len(body.NewPassword) < 6 {
		fail(c, http.StatusBadRequest, auth.ErrWeakPassword.Error())
		return
	}
	h, err := auth.HashPassword(body.NewPassword)
	if err != nil {
		fail(c, http.StatusInternalServerError, "hash failed")
		return
	}
	_ = s.db.SetSetting(setAdminHash, h)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// GET /api/v1/auth/view
func (s *Server) handleGetViewSettings(c *gin.Context) {
	ve, _, _ := s.db.GetSetting(setViewEnabled)
	vh, _, _ := s.db.GetSetting(setViewHash)
	c.JSON(http.StatusOK, gin.H{
		"enabled":           ve == "1",
		"password_separate": vh != "",
	})
}

// PUT /api/v1/auth/view {enabled, password?}
func (s *Server) handlePutViewSettings(c *gin.Context) {
	var body struct {
		Enabled  bool   `json:"enabled"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		fail(c, http.StatusBadRequest, "invalid json")
		return
	}
	if body.Enabled && len(body.Password) > 0 && len(body.Password) < 6 {
		fail(c, http.StatusBadRequest, auth.ErrWeakPassword.Error())
		return
	}
	if body.Password != "" {
		h, err := auth.HashPassword(body.Password)
		if err != nil {
			fail(c, http.StatusInternalServerError, "hash failed")
			return
		}
		_ = s.db.SetSetting(setViewHash, h)
	}
	on := "0"
	if body.Enabled {
		on = "1"
	}
	_ = s.db.SetSetting(setViewEnabled, on)
	c.JSON(http.StatusOK, gin.H{"ok": true, "enabled": body.Enabled})
}

// GET /api/v1/auth/join-key — 不存在时自动生成
func (s *Server) handleGetJoinKey(c *gin.Context) {
	v, ok, _ := s.db.GetSetting(setJoinKey)
	if !ok || v == "" {
		v = auth.RandomHex(16)
		_ = s.db.SetSetting(setJoinKey, v)
	}
	c.JSON(http.StatusOK, gin.H{"join_key": v})
}

// POST /api/v1/auth/join-key/rotate
func (s *Server) handleRotateJoinKey(c *gin.Context) {
	v := auth.RandomHex(16)
	_ = s.db.SetSetting(setJoinKey, v)
	c.JSON(http.StatusOK, gin.H{"join_key": v})
}

func (s *Server) setSession(c *gin.Context, scope string) {
	tok := s.authm.Issue(scope, sessionTTL)
	// CSRF（T-12）：SameSite=Lax —— 跨站 POST/PUT/DELETE 不携带会话 Cookie
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(auth.CookieName, tok, int(sessionTTL.Seconds()), "/", "", false, true)
}
