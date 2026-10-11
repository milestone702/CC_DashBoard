// 正向连接（方向 pull）：Master 定时 GET Agent 的 /metrics 拉取指标。
package collector

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"time"

	"cc_dashboard/server/internal/model"
)

// pullPayload 与 Agent /report 上报体同构。
type pullPayload struct {
	CPU      float64         `json:"cpu"`
	MemUsed  int64           `json:"mem_used"`
	MemTotal int64           `json:"mem_total"`
	DiskPct  float64         `json:"disk_pct"`
	NetRx    int64           `json:"net_rx"`
	NetTx    int64           `json:"net_tx"`
	Load1    float64         `json:"load1"`
	Uptime   int64           `json:"uptime"`
	Detail   json.RawMessage `json:"detail,omitempty"`
	Live     json.RawMessage `json:"live,omitempty"`
	Hardware json.RawMessage `json:"hardware,omitempty"`
}

var pullClient = &http.Client{Timeout: 10 * time.Second}

// scanPull 扫描 direction=pull 的 Agent 模式节点并拉取。
func (m *Manager) scanPull(now int64) {
	nodes, err := m.db.ListNodes()
	if err != nil {
		return
	}
	for _, n := range nodes {
		if n.AccessMode != model.ModeAgent || n.Direction != "pull" || !n.Enabled || !n.Approved {
			continue
		}
		interval := n.ReportInterval
		if interval <= 0 {
			interval = 5
		}
		if n.LastSeen > 0 && now-n.LastSeen < int64(interval)-1 {
			continue
		}
		m.pull(n)
	}
}

// pull 拉取单节点：GET http://address:port/metrics（Header 携带节点 Token 供 Agent 校验）。
func (m *Manager) pull(n *model.Node) {
	port := n.Port
	if port <= 0 {
		port = 9100 // Agent listen 默认端口
	}
	url := "http://" + net.JoinHostPort(n.Address, fmt.Sprintf("%d", port)) + "/metrics"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return
	}
	req.Header.Set("X-CC-Node-Token", n.Token)
	resp, err := pullClient.Do(req)
	if err != nil {
		log.Printf("PULL[%s] 连接失败 %s: %v", n.Name, url, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		log.Printf("PULL[%s] 返回 %d", n.Name, resp.StatusCode)
		return
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	if err != nil {
		return
	}
	var p pullPayload
	if err := json.Unmarshal(body, &p); err != nil {
		log.Printf("PULL[%s] 响应解析失败: %v", n.Name, err)
		return
	}
	now := time.Now().Unix()
	sample := &model.MetricsSample{
		NodeID: n.ID, Timestamp: now,
		CPU: p.CPU, MemUsed: p.MemUsed, MemTotal: p.MemTotal,
		DiskPct: p.DiskPct, NetRx: p.NetRx, NetTx: p.NetTx,
		Load1: p.Load1, Uptime: p.Uptime,
	}
	m.ingest(n, sample, string(p.Detail), string(p.Hardware), p.Live)
}
