// 健康检查端点：供容器 liveness/readiness 探针与反代使用，不暴露业务数据。
package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// GET /healthz — 进程存活 + 数据库可用。
// 数据库不可用时返回 503，便于编排系统重启或摘除实例。
func (s *Server) handleHealthz(c *gin.Context) {
	if err := s.db.Ping(); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "degraded", "db": "down"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
