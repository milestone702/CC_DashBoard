// REST / WebSocket 路由。接口契约见 docs/API.md。
package api

import (
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"cc_dashboard/server/internal/alert"
	"cc_dashboard/server/internal/auth"
	"cc_dashboard/server/internal/store"
	"cc_dashboard/server/internal/webui"
)

// Server 持有各 handler 共享依赖。
type Server struct {
	db           *store.DB
	key          []byte // AES-256-GCM 主密钥
	hub          *Hub
	engine       *alert.Engine
	authm        *auth.Manager // 用户会话签发/校验
	limiter      *loginLimiter // 登录失败限流
	upgrader     websocket.Upgrader
	livesMu      sync.RWMutex // 实时明细缓存
	lives        map[int64]json.RawMessage
	upMu         sync.Mutex     // 手动升级标记
	upgradeForce map[int64]bool // 节点 ID → 下次上报强制下发 upgrade 指令
	rollupMu     sync.Mutex     // 查询侧降采样节流
	lastRollup   int64          // 上次降采样的 Unix 秒
}

// NewRouter 组装全部路由。返回 (路由, 服务实例)——服务实例用于注入 live 缓存回调。
func NewRouter(db *store.DB, key []byte, hub *Hub, engine *alert.Engine, webDir string) (*gin.Engine, *Server) {
	s := &Server{
		db:           db,
		key:          key,
		hub:          hub,
		engine:       engine,
		authm:        auth.NewManager(key),
		limiter:      newLoginLimiter(),
		lives:        map[int64]json.RawMessage{},
		upgradeForce: map[int64]bool{},
		upgrader: websocket.Upgrader{
			// 浏览器握手会带 Origin：要求与 Host 同源（防 WS 跨站劫持）；
			// 原生客户端（App / 脚本）不发送 Origin，直接放行。
			CheckOrigin: func(r *http.Request) bool {
				origin := r.Header.Get("Origin")
				if origin == "" {
					return true
				}
				u, err := url.Parse(origin)
				if err != nil || u.Host == "" {
					return false
				}
				host := r.Host
				if fh := r.Header.Get("X-Forwarded-Host"); fh != "" {
					host = strings.TrimSpace(strings.Split(fh, ",")[0])
				}
				return u.Host == host
			},
		},
	}

	r := gin.Default()
	r.Use(corsMiddleware)
	r.Use(gzipMiddleware()) // 移动网络下显著减少传输量

	v1 := r.Group("/api/v1", csrfGuard(), s.auditMiddleware())
	{
		// ---- 公开：机器上报 / 自注册 / 用户认证 ----
		v1.POST("/report", s.handleReport)
		v1.POST("/register", s.handleRegister)
		v1.GET("/auth/status", s.handleAuthStatus)
		v1.GET("/site", s.handleGetSite)
		v1.GET("/auth/whoami", s.handleWhoami)
		v1.POST("/auth/setup", s.loginGuard(), s.handleSetup)
		v1.POST("/auth/login", s.loginGuard(), s.handleLogin)
		v1.POST("/auth/logout", s.handleLogout)
		v1.POST("/auth/refresh", s.handleRefresh)

		// ---- 管理员会话 ----
		v1.POST("/auth/password", s.requireAdmin, s.handleChangePassword)
		v1.PUT("/site", s.requireAdmin, s.handleSetSite)
		v1.PUT("/overview/cols", s.requireAdmin, s.handleSetOverviewCols)
		v1.GET("/auth/view", s.requireAdmin, s.handleGetViewSettings)
		v1.PUT("/auth/view", s.requireAdmin, s.handlePutViewSettings)
		v1.GET("/auth/join-key", s.requireAdmin, s.handleGetJoinKey)
		v1.POST("/auth/join-key/rotate", s.requireAdmin, s.handleRotateJoinKey)

		// Prometheus 抓取令牌（可选；设置后 /metrics 需带 token）
		v1.GET("/auth/metrics-token", s.requireAdmin, s.handleGetMetricsToken)
		v1.PUT("/auth/metrics-token", s.requireAdmin, s.handleSetMetricsToken)

		// API Key（Bearer 通道）：App / 脚本等非浏览器客户端凭证
		v1.GET("/auth/api-key", s.requireAdmin, s.handleGetAPIKey)
		v1.POST("/auth/api-key/rotate", s.requireAdmin, s.handleRotateAPIKey)
		v1.DELETE("/auth/api-key", s.requireAdmin, s.handleDeleteAPIKey)

		// 多凭证管理（每台设备/脚本一条，互不影响）
		v1.GET("/auth/api-keys", s.requireAdmin, s.handleListAPIKeys)
		v1.POST("/auth/api-keys", s.requireAdmin, s.handleCreateAPIKey)
		v1.DELETE("/auth/api-keys/:id", s.requireAdmin, s.handleDeleteAPIKeyByID)

		// 告警通知配置（管理员）
		v1.GET("/notify/config", s.requireAdmin, s.handleGetNotifyConfig)
		v1.PUT("/notify/config", s.requireAdmin, s.handlePutNotifyConfig)
		v1.POST("/notify/test", s.requireAdmin, s.handleNotifyTest)
		v1.GET("/notify/logs", s.requireAdmin, s.handleListNotifyLogs) // 发送记录

		// ---- 读接口（开启访问密码时需 view/admin 会话）----
		v1.GET("/summary", s.requireView, s.handleSummary)
		v1.GET("/overview/cols", s.requireView, s.handleGetOverviewCols)
		v1.GET("/nodes", s.requireView, s.handleListNodes)
		v1.GET("/nodes/:id", s.requireView, s.handleGetNode)
		v1.GET("/nodes/:id/detail", s.requireView, s.handleNodeDetail)
		v1.GET("/nodes/:id/history", s.requireView, s.handleHistory)
		v1.GET("/nodes/:id/install-script", s.requireAdmin, s.handleInstallScript)
		v1.GET("/alerts", s.requireView, s.handleListAlerts)
		v1.GET("/alerts/rules", s.requireView, s.handleListRules)
		v1.GET("/ws", s.requireViewWS, s.handleWebSocket)

		// ---- 管理员写接口 ----
		v1.POST("/nodes", s.requireAdmin, s.handleCreateNode)
		v1.POST("/nodes/reorder", s.requireAdmin, s.handleReorderNodes)
		v1.PUT("/nodes/:id", s.requireAdmin, s.handleUpdateNode)
		v1.DELETE("/nodes/:id", s.requireAdmin, s.handleDeleteNode)
		v1.POST("/nodes/:id/upgrade", s.requireAdmin, s.handleForceUpgrade)
		v1.POST("/alerts/rules", s.requireAdmin, s.handleCreateRule)
		v1.PUT("/alerts/rules/:id", s.requireAdmin, s.handleUpdateRule)
		v1.DELETE("/alerts/rules/:id", s.requireAdmin, s.handleDeleteRule)

		// 配置导出 / 恢复（不含 Token / 凭据 / 密钥；恢复只新增不覆盖）
		v1.GET("/backup", s.requireAdmin, s.handleBackupExport)
		v1.POST("/restore", s.requireAdmin, s.handleRestore)

		// 操作审计（管理员写操作的追溯记录）
		v1.GET("/audit", s.requireAdmin, s.handleListAudit)

		// ---- 程序池（管理端 CRUD + OTA 开关；下载走节点 Token）----
		v1.GET("/pool", s.requireAdmin, s.handlePoolList)
		v1.POST("/pool/upload", s.requireAdmin, s.handlePoolUpload)
		v1.POST("/pool/batch", s.requireAdmin, s.handlePoolBatch)
		v1.PUT("/pool/ota", s.requireAdmin, s.handlePoolOTASet)
		v1.DELETE("/pool/:id", s.requireAdmin, s.handlePoolDelete)
		v1.GET("/pool/:id/download", s.handlePoolDownload)
	}

	mountWeb(r, webDir)

	// Prometheus 指标导出（根路径惯例；反代公网部署时于代理层限制来源）
	r.GET("/metrics", s.handleMetricsExport)
	// 健康检查（容器探针 / 反代上游检测）
	r.GET("/healthz", s.handleHealthz)
	return r, s
}

// mountWeb 托管前端：-web 外部目录优先（开发热覆盖），否则使用编译嵌入的产物（单二进制部署）。
func mountWeb(r *gin.Engine, webDir string) {
	if webDir != "" {
		idx := filepath.Join(webDir, "index.html")
		if _, err := os.Stat(idx); err == nil {
			mountExternal(r, idx, filepath.Join(webDir, "assets"))
			return
		}
		log.Printf("前端目录不可用（-web %s），回退嵌入产物", webDir)
	}
	mountEmbedded(r)
}

func mountExternal(r *gin.Engine, idx string, assets string) {
	r.StaticFile("/", idx)
	r.Static("/assets", assets)
	// 根级静态文件（favicon 等，位于前端产物目录）
	if fav := filepath.Join(filepath.Dir(idx), "favicon.ico"); fileExists(fav) {
		r.StaticFile("/favicon.ico", fav)
	}
	r.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.JSON(http.StatusNotFound, gin.H{"message": "not found"})
			return
		}
		c.File(idx) // /device/1 等前端路由回退到 index.html
	})
	log.Printf("前端托管于外部目录: %s", idx)
}

func mountEmbedded(r *gin.Engine) {
	fsys, err := fs.Sub(webui.Dist, "dist")
	if err != nil {
		log.Printf("嵌入前端不可用: %v", err)
		return
	}
	index, err := fs.ReadFile(fsys, "index.html")
	if err != nil {
		log.Printf("无嵌入前端产物（先执行 build.ps1 同步 web/dist）")
		return
	}
	// 单一回退处理器：静态资源按扩展名精确返回，根级静态文件（favicon 等）次之，其余路径回退 index（SPA 路由）
	r.NoRoute(func(c *gin.Context) {
		p := c.Request.URL.Path
		if strings.HasPrefix(p, "/api/") {
			c.JSON(http.StatusNotFound, gin.H{"message": "not found"})
			return
		}
		if strings.HasPrefix(p, "/assets/") {
			name := strings.TrimPrefix(p, "/")
			if data, rerr := fs.ReadFile(fsys, name); rerr == nil {
				// 文件名带内容 hash，可长期缓存（移动端二次访问几乎零流量）
				c.Header("Cache-Control", "public, max-age=31536000, immutable")
				c.Data(http.StatusOK, mimeByExt(filepath.Ext(p)), data)
				return
			}
		}
		// 根级带扩展名的文件（如 /favicon.ico）：存在则返回，避免被 SPA 回退成 HTML
		if !strings.Contains(strings.TrimPrefix(p, "/"), "/") && strings.Contains(filepath.Base(p), ".") {
			name := strings.TrimPrefix(p, "/")
			if data, rerr := fs.ReadFile(fsys, name); rerr == nil {
				c.Header("Cache-Control", "public, max-age=86400")
				c.Data(http.StatusOK, mimeByExt(filepath.Ext(p)), data)
				return
			}
		}
		// 入口 HTML 必须每次校验，否则发新版后移动端会一直用旧缓存
		c.Header("Cache-Control", "no-cache")
		c.Data(http.StatusOK, "text/html; charset=utf-8", index)
	})
	log.Printf("嵌入前端已启用（单二进制模式）")
}

// mimeByExt 显式映射，避免依赖系统 MIME 注册表。
func mimeByExt(ext string) string {
	switch ext {
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".html":
		return "text/html; charset=utf-8"
	case ".json", ".map":
		return "application/json"
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".ico":
		return "image/x-icon"
	case ".woff":
		return "font/woff"
	case ".woff2":
		return "font/woff2"
	default:
		return "application/octet-stream"
	}
}

// csrfGuard CSRF 显式防护（T-12）：非简单方法（POST/PUT/DELETE/PATCH）若携带 Origin 头，
// 必须与请求 Host 同源，否则 403。无 Origin（curl / Agent 上报 / 原生客户端）放行——
// 配合会话 Cookie 的 SameSite=Lax（双保险）：跨站表单即便发出也拿不到会话。
// 携 Bearer API Key 的请求直接放行：该凭证由客户端显式设置，浏览器不会自动附带，天然免疫 CSRF。
// 反代部署时 Host 为代理转发的虚拟主机名，Origin 与之一致，行为不变。
func csrfGuard() gin.HandlerFunc {
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			c.Next()
			return
		}
		if bearerToken(c) != "" {
			c.Next()
			return
		}
		if origin := c.GetHeader("Origin"); origin != "" {
			u, err := url.Parse(origin)
			// 反代场景 Host 可能是 upstream 地址：优先比对 X-Forwarded-Host（逗号列表取首个）
			reqHost := c.Request.Host
			if fh := c.GetHeader("X-Forwarded-Host"); fh != "" {
				reqHost = strings.TrimSpace(strings.Split(fh, ",")[0])
			}
			if err != nil || u.Host == "" || u.Host != reqHost {
				fail(c, http.StatusForbidden, "跨站请求被拒绝（CSRF Origin 校验失败）")
				c.Abort()
				return
			}
		}
		c.Next()
	}
}

func corsMiddleware(c *gin.Context) {
	c.Header("Access-Control-Allow-Origin", "*")
	c.Header("Access-Control-Allow-Headers", "Content-Type, X-CC-Node-Token, Authorization")
	c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
	if c.Request.Method == http.MethodOptions {
		c.AbortWithStatus(http.StatusNoContent)
		return
	}
	c.Next()
}

func fail(c *gin.Context, code int, msg string) {
	c.JSON(code, gin.H{"message": msg})
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
