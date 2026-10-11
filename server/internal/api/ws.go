// GET /api/v1/ws — 实时推流。
package api

import (
	"github.com/gin-gonic/gin"
)

// handleWebSocket 升级为 WebSocket 并接入 Hub。
// 消息格式见 docs/API.md「WebSocket 协议」。
func (s *Server) handleWebSocket(c *gin.Context) {
	conn, err := s.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return // Upgrade 已发送错误响应
	}
	s.hub.serveWS(conn)
}
