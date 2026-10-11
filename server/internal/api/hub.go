// WebSocket 实时推流集线器。
package api

import (
	"encoding/json"
	"log"
	"sync"

	"github.com/gorilla/websocket"
)

// Hub 管理全部 WS 连接并广播消息。
type Hub struct {
	mu      sync.Mutex
	clients map[*wsClient]struct{}
}

type wsClient struct {
	conn *websocket.Conn
	send  chan []byte
}

// NewHub 创建集线器。
func NewHub() *Hub {
	return &Hub{clients: map[*wsClient]struct{}{}}
}

// Run 丢弃广播协程占位（当前 Broadcast 直接投递，无需事件循环）。
func (h *Hub) Run() {}

// Broadcast 向所有客户端推送 JSON；单客户端阻塞时丢弃该条消息。
func (h *Hub) Broadcast(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		select {
		case c.send <- data:
		default: // 缓冲满：丢弃，避免拖慢上报链路
		}
	}
}

func (h *Hub) add(c *wsClient) {
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
}

func (h *Hub) remove(c *wsClient) {
	h.mu.Lock()
	if _, ok := h.clients[c]; ok {
		delete(h.clients, c)
		close(c.send)
	}
	h.mu.Unlock()
}

// ClientCount 当前连接数（用于 /summary 观测）。
func (h *Hub) ClientCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

// serveWS 处理 /api/v1/ws 升级与读写泵。
func (h *Hub) serveWS(conn *websocket.Conn) {
	c := &wsClient{conn: conn, send: make(chan []byte, 64)}
	h.add(c)
	go h.writePump(c)
	h.readPump(c) // 阻塞直到连接关闭
	h.remove(c)
}

func (h *Hub) writePump(c *wsClient) {
	for data := range c.send {
		if err := c.conn.WriteMessage(websocket.TextMessage, data); err != nil {
			return
		}
	}
}

func (h *Hub) readPump(c *wsClient) {
	defer c.conn.Close()
	for {
		if _, _, err := c.conn.ReadMessage(); err != nil {
			return
		}
	}
}

func logWSDrop(err error) {
	if err != nil {
		log.Printf("ws: %v", err)
	}
}
