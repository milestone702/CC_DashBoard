// 操作审计：记录管理员的写操作（POST/PUT/DELETE 且成功）。
// 不记录读操作，避免把日志刷成噪声；写记录异步落库，不影响接口响应时间。
package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"cc_dashboard/server/internal/auth"
	"cc_dashboard/server/internal/store"
)

// auditMiddleware 挂在 /api/v1 组上：只记「已认证为 admin 且成功」的写操作。
func (s *Server) auditMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		m := c.Request.Method
		if m != http.MethodPost && m != http.MethodPut && m != http.MethodDelete {
			c.Next()
			return
		}
		// 在 handler 之前取身份：handler 里可能清 Cookie/改状态
		scope, via := s.callerScope(c)
		c.Next()
		if scope != auth.ScopeAdmin || c.Writer.Status() >= 400 {
			return
		}
		actor := "admin(cookie)"
		if via == "key" {
			actor = "admin(key)"
		}
		actorPath := c.Request.URL.Path
		status := c.Writer.Status()
		ip := c.ClientIP()
		go func() { // 异步：审计失败不影响业务
			_ = s.db.InsertAudit(actor, m, actorPath, status, ip)
		}()
	}
}

// GET /api/v1/audit?limit=100 — 最近的管理员操作记录
func (s *Server) handleListAudit(c *gin.Context) {
	limit := 100
	if v, err := strconvParseInt(c.Query("limit")); err == nil && v > 0 && v <= 500 {
		limit = int(v)
	}
	logs, err := s.db.ListAudit(limit)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	if logs == nil {
		logs = []*store.AuditLog{}
	}
	c.JSON(http.StatusOK, gin.H{"logs": logs})
}
