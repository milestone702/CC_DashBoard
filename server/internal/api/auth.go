// 用户侧认证与授权：初始化/登录/会话/密码与视图密码管理、API Key、中间件。
package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"cc_dashboard/server/internal/auth"
	"cc_dashboard/server/internal/store"
)

// settings 键名
const (
	setAdminHash    = "admin_password_hash"
	setViewEnabled  = "view_password_enabled"
	setViewHash     = "view_password_hash"
	setJoinKey      = "join_key"
	setMetricsToken = "metrics_token" // 非空时 /metrics 需要 ?token= 或 Bearer
)

const sessionTTL = 7 * 24 * time.Hour

// ---------- 中间件 ----------

// sessionScope 浏览器会话 Cookie 的作用域。
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

// bearerToken 从 Authorization: Bearer <key> 中提取凭证（前缀大小写不敏感）。
func bearerToken(c *gin.Context) string {
	h := c.GetHeader("Authorization")
	if len(h) < 7 || !strings.EqualFold(h[:7], "bearer ") {
		return ""
	}
	return strings.TrimSpace(h[7:])
}

// apiKeyScope 校验 Bearer API Key 并返回其作用域；无凭证或无效返回空串。
// 供 App / 脚本等无法托管 Cookie 的客户端使用；密钥仅存哈希，按哈希等值命中唯一索引。
func (s *Server) apiKeyScope(c *gin.Context) string {
	tok := bearerToken(c)
	if tok == "" {
		return ""
	}
	k, err := s.db.FindAPIKeyByHash(auth.HashAPIKey(tok))
	if err != nil || k == nil {
		return ""
	}
	if k.Scope != auth.ScopeAdmin {
		return auth.ScopeView // 缺省/异常值一律按最小权限处理
	}
	return k.Scope
}

// callerScope 统一身份判定：优先 Bearer API Key，其次会话 Cookie。
// via 为凭证类型（"key" / "cookie"），用于 whoami 回显与排障。
func (s *Server) callerScope(c *gin.Context) (scope, via string) {
	if sc := s.apiKeyScope(c); sc != "" {
		return sc, "key"
	}
	if sc := s.sessionScope(c); sc != "" {
		return sc, "cookie"
	}
	return "", ""
}

func (s *Server) isAdmin(c *gin.Context) bool {
	scope, _ := s.callerScope(c)
	return scope == auth.ScopeAdmin
}

func (s *Server) viewEnabled() bool {
	v, _, err := s.db.GetSetting(setViewEnabled)
	return err == nil && v == "1"
}

// requireAdmin 后台写操作：必须 admin 权限（Bearer API Key 或 admin 会话）。
func (s *Server) requireAdmin(c *gin.Context) {
	if !s.isAdmin(c) {
		fail(c, http.StatusUnauthorized, "需要管理员登录")
		c.Abort() // 必须中止，否则后续 handler 仍会执行
		return
	}
	c.Next()
}

// requireView 前台读操作：持有有效凭证即放行；开启访问密码且无凭证时拒绝。
func (s *Server) requireView(c *gin.Context) {
	if scope, _ := s.callerScope(c); scope != "" && auth.Allows(scope, auth.ScopeView) {
		c.Next()
		return
	}
	if !s.viewEnabled() {
		c.Next()
		return
	}
	fail(c, http.StatusUnauthorized, "需要访问密码")
	c.Abort()
}

// requireViewWS WebSocket 握手的读权限校验：部分 WS 客户端无法设置自定义请求头，
// 额外允许 ?access_token=<key> 传入同一凭证（仅此端点接受 query 传参）。
func (s *Server) requireViewWS(c *gin.Context) {
	if c.GetHeader("Authorization") == "" {
		if t := c.Query("access_token"); t != "" {
			c.Request.Header.Set("Authorization", "Bearer "+t)
		}
	}
	s.requireView(c)
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

// GET /api/v1/auth/whoami — 当前身份：scope 为空=未认证，via 表示凭证类型。
func (s *Server) handleWhoami(c *gin.Context) {
	scope, via := s.callerScope(c)
	c.JSON(http.StatusOK, gin.H{"scope": scope, "via": via})
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
	s.loginSucceeded(c)
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
			s.loginFailed(c)
			fail(c, http.StatusUnauthorized, "密码错误")
			return
		}
		s.loginSucceeded(c)
		s.setSession(c, auth.ScopeView)
		c.JSON(http.StatusOK, gin.H{"ok": true, "scope": auth.ScopeView})
	default: // admin
		if adminHash == "" {
			fail(c, http.StatusBadRequest, "请先初始化管理员密码")
			return
		}
		if !auth.CheckPassword(adminHash, body.Password) {
			s.loginFailed(c)
			fail(c, http.StatusUnauthorized, "密码错误")
			return
		}
		s.loginSucceeded(c)
		s.setSession(c, auth.ScopeAdmin)
		c.JSON(http.StatusOK, gin.H{"ok": true, "scope": auth.ScopeAdmin})
	}
}

// POST /api/v1/auth/refresh — 会话续期：用有效会话换一张新 Cookie（再延 7 天）。
// 供长时间开着的浏览器页使用；会话已过期返回 401（需重新登录）。
// 注意：Bearer API Key 本身不过期，无需调用此接口。
func (s *Server) handleRefresh(c *gin.Context) {
	scope := s.sessionScope(c)
	if scope == "" {
		fail(c, http.StatusUnauthorized, "会话已过期，请重新登录")
		return
	}
	s.setSession(c, scope)
	c.JSON(http.StatusOK, gin.H{"ok": true, "scope": scope})
}

// ---------- Prometheus 抓取令牌 ----------

// GET /api/v1/auth/metrics-token — 是否已设置（不回传明文）
func (s *Server) handleGetMetricsToken(c *gin.Context) {
	v, _, _ := s.db.GetSetting(setMetricsToken)
	c.JSON(http.StatusOK, gin.H{"set": v != ""})
}

// PUT /api/v1/auth/metrics-token {token} — 空串=关闭校验（/metrics 恢复开放）
func (s *Server) handleSetMetricsToken(c *gin.Context) {
	var body struct {
		Token string `json:"token"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		fail(c, http.StatusBadRequest, "invalid json")
		return
	}
	v := strings.TrimSpace(body.Token)
	if v == "" {
		_ = s.db.DeleteSetting(setMetricsToken)
		c.JSON(http.StatusOK, gin.H{"ok": true, "set": false})
		return
	}
	if err := s.db.SetSetting(setMetricsToken, v); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "set": true})
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

// ---------- API Key（多凭证：App / 脚本 / 多设备各自持有） ----------

// defaultKeyName 旧版单 Key 接口操作的记录名，保证老接口/老客户端继续可用。
const defaultKeyName = "default"

// GET /api/v1/auth/api-key — 兼容旧接口：仅反映 default 凭证是否存在。
func (s *Server) handleGetAPIKey(c *gin.Context) {
	k, err := s.db.FirstAPIKeyByName(defaultKeyName)
	if err != nil || k == nil {
		c.JSON(http.StatusOK, gin.H{"set": false, "scope": ""})
		return
	}
	scope := k.Scope
	if scope != auth.ScopeAdmin {
		scope = auth.ScopeView
	}
	c.JSON(http.StatusOK, gin.H{"set": true, "scope": scope})
}

// POST /api/v1/auth/api-key/rotate {scope?} — 兼容旧接口：重置 default 凭证。
// 明文仅在此响应中出现一次，服务端只存 SHA-256；旧 default Key 立即失效。
func (s *Server) handleRotateAPIKey(c *gin.Context) {
	var body struct {
		Scope string `json:"scope"`
	}
	_ = c.ShouldBindJSON(&body) // 允许空请求体：缺省 admin
	scope := strings.TrimSpace(body.Scope)
	if scope == "" {
		scope = auth.ScopeAdmin
	}
	if scope != auth.ScopeAdmin && scope != auth.ScopeView {
		fail(c, http.StatusBadRequest, "scope 仅支持 admin / view")
		return
	}
	key := auth.NewAPIKey()
	if _, err := s.db.DeleteAPIKeysByName(defaultKeyName); err != nil {
		fail(c, http.StatusInternalServerError, "reset api key failed")
		return
	}
	if _, err := s.db.CreateAPIKey(defaultKeyName, scope, auth.HashAPIKey(key)); err != nil {
		fail(c, http.StatusInternalServerError, "save api key failed")
		return
	}
	c.JSON(http.StatusOK, gin.H{"api_key": key, "scope": scope})
}

// ---------- 多凭证管理 ----------

// GET /api/v1/auth/api-keys — 列出全部凭证（不含明文与哈希）。
func (s *Server) handleListAPIKeys(c *gin.Context) {
	keys, err := s.db.ListAPIKeys()
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	if keys == nil {
		keys = []*store.APIKey{}
	}
	c.JSON(http.StatusOK, gin.H{"keys": keys})
}

// POST /api/v1/auth/api-keys {name, scope} — 新建凭证，明文仅返回一次。
func (s *Server) handleCreateAPIKey(c *gin.Context) {
	var body struct {
		Name  string `json:"name"`
		Scope string `json:"scope"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		fail(c, http.StatusBadRequest, "invalid json")
		return
	}
	name := strings.TrimSpace(body.Name)
	if r := []rune(name); len(r) > 32 {
		name = string(r[:32])
	}
	if name == "" {
		name = "device"
	}
	scope := strings.TrimSpace(body.Scope)
	if scope == "" {
		scope = auth.ScopeView
	}
	if scope != auth.ScopeAdmin && scope != auth.ScopeView {
		fail(c, http.StatusBadRequest, "scope 仅支持 admin / view")
		return
	}
	key := auth.NewAPIKey()
	k, err := s.db.CreateAPIKey(name, scope, auth.HashAPIKey(key))
	if err != nil {
		fail(c, http.StatusInternalServerError, "save api key failed")
		return
	}
	c.JSON(http.StatusOK, gin.H{"key": k, "api_key": key})
}

// DELETE /api/v1/auth/api-keys/:id — 吊销指定凭证。
func (s *Server) handleDeleteAPIKeyByID(c *gin.Context) {
	id, err := strconvParseInt(c.Param("id"))
	if err != nil {
		fail(c, http.StatusBadRequest, "invalid id")
		return
	}
	ok, err := s.db.DeleteAPIKey(id)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		fail(c, http.StatusNotFound, "api key not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// DELETE /api/v1/auth/api-key — 兼容旧接口：吊销 default 凭证（删后立即 401）。
func (s *Server) handleDeleteAPIKey(c *gin.Context) {
	if _, err := s.db.DeleteAPIKeysByName(defaultKeyName); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) setSession(c *gin.Context, scope string) {
	tok := s.authm.Issue(scope, sessionTTL)
	// CSRF（T-12）：SameSite=Lax —— 跨站 POST/PUT/DELETE 不携带会话 Cookie
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(auth.CookieName, tok, int(sessionTTL.Seconds()), "/", "", false, true)
}
