// POST /api/v1/report — Agent 指标上报（模式 A，反向连接默认）。
package api

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"cc_dashboard/server/internal/model"
	"cc_dashboard/server/internal/store"
)

// reportPayload 上报体：定宽样本 + 系统详情 + 实时明细 + 硬件清单 + 身份元数据。
type reportPayload struct {
	model.MetricsSample
	Detail   json.RawMessage `json:"detail,omitempty"`
	Live     json.RawMessage `json:"live,omitempty"`
	Hardware json.RawMessage `json:"hardware,omitempty"`
	// 身份元数据（新版 Agent 上报；旧 Agent 全为空，不影响兼容）
	AgentVersion string            `json:"agent_version,omitempty"`
	AgentGoos    string            `json:"agent_goos,omitempty"`
	AgentArch    string            `json:"agent_arch,omitempty"`
	PublicIP     string            `json:"public_ip,omitempty"`
	AgentMem     int64             `json:"agent_mem,omitempty"` // Agent 自身内存占用（字节）
	BootEvents   []model.BootEvent `json:"boot_events,omitempty"`
}

func (s *Server) handleReport(c *gin.Context) {
	node, err := s.db.FindNodeByToken(c.GetHeader("X-CC-Node-Token"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			fail(c, http.StatusUnauthorized, "invalid token")
			return
		}
		fail(c, http.StatusInternalServerError, "token lookup failed")
		return
	}
	if !node.Enabled {
		fail(c, http.StatusForbidden, "node disabled")
		return
	}
	// 入网审批：pending 节点的上报被拒绝，后台批准后自动恢复
	if !node.Approved {
		fail(c, http.StatusForbidden, "pending approval")
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 256<<10)
	var p reportPayload
	if err := c.ShouldBindJSON(&p); err != nil {
		fail(c, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}

	// ts / node_id 一律以服务端为准
	sample := p.MetricsSample
	sample.NodeID = node.ID
	// 温度：Agent 未在顶层给出时，从 live.temps 里提取最高值（统一在服务端处理，
	// 采集端只管往 live 里写，不必各自改上报格式）
	if sample.TempMax == 0 {
		sample.TempMax = model.ExtractTempMax(p.Live)
	}
	if err := s.db.InsertSample(node.ID, &sample); err != nil {
		fail(c, http.StatusInternalServerError, "store sample failed")
		return
	}

	// 身份元数据：版本/目标平台（供程序池匹配与可升级提示）
	if p.AgentVersion != "" || p.AgentGoos != "" || p.AgentArch != "" {
		_ = s.db.UpdateNodeAgentMeta(node.ID, p.AgentVersion, p.AgentGoos, p.AgentArch)
	}
	// Agent 自监控：自身内存占用
	if p.AgentMem > 0 {
		_ = s.db.SetNodeAgentMem(node.ID, p.AgentMem)
	}
	// 公网 IP：优先上报连接的源 IP（公网时），否则取 Agent 上报值（公网时）；均无则保留原值
	if ip := resolvePublicIP(c.Request.RemoteAddr, p.PublicIP); ip != "" {
		_ = s.db.SetNodePublicIP(node.ID, ip)
	}
	// 开关机记录（INSERT OR IGNORE 按 (node_id, ts, type) 去重）
	if len(p.BootEvents) > 0 {
		_ = s.db.InsertBootEvents(node.ID, p.BootEvents)
	}

	if len(p.Detail) > 0 && string(p.Detail) != "null" {
		_ = s.db.SaveNodeDetail(node.ID, string(p.Detail))
	}
	if len(p.Hardware) > 0 && string(p.Hardware) != "null" {
		_ = s.db.SaveNodeHardware(node.ID, string(p.Hardware))
	}

	// 实时明细：内存缓存 + WS 推送
	if len(p.Live) > 0 && string(p.Live) != "null" {
		s.setLive(node.ID, p.Live)
		s.hub.Broadcast(gin.H{"type": "live", "node_id": node.ID, "data": p.Live})
	}

	// 状态流转：首次/恢复上线 → online + 解除离线告警 + WS 推送
	if changed, err := s.db.SetNodeOnline(node.ID); err == nil && changed {
		s.engine.SetOnline(node.ID)
	}

	// 阈值评估（同步，保证前端立即收到 firing）
	s.engine.EvaluateSample(node.ID, &sample)

	// 实时推流：{"type":"metrics", ...}
	s.hub.Broadcast(gin.H{
		"type":    "metrics",
		"node_id": node.ID,
		"data":    sample,
	})

	resp := gin.H{"ok": true, "ts": sample.Timestamp}
	// OTA 升级指令：全局开关开启，或管理员手动触发（单节点一次性强制）；OpenWrt 不下发
	force := s.consumeUpgradeForce(node.ID)
	if node.OSType != model.OSTypeOpenWrt {
		if up := s.agentOTAUpgrade(p.AgentGoos, p.AgentArch, p.AgentVersion, force); up != nil {
			resp["upgrade"] = gin.H{
				"version": up.Version,
				"url":     poolDownloadURL(up.ID),
				"sha256":  up.SHA256,
				"size":    up.Size,
			}
		}
	}
	c.JSON(http.StatusOK, resp)
}

// resolvePublicIP 解析公网 IP：优先上报连接的源 IP，其次 Agent 自查上报值；非公网地址返回空串。
func resolvePublicIP(remoteAddr, reported string) string {
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		if ip := net.ParseIP(host); ip != nil && isPublicIP(ip) {
			return ip.String()
		}
	}
	// RemoteAddr 为内网（同 LAN / 隧道直连）时兜底 Agent 查得的出口公网 IP
	if ip := net.ParseIP(strings.TrimSpace(reported)); ip != nil && isPublicIP(ip) {
		return ip.String()
	}
	return ""
}

func isPublicIP(ip net.IP) bool {
	return ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast()
}
