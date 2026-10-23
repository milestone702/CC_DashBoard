// 节点管理：查询、创建、更新、删除、详情与安装指令。
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"cc_dashboard/server/internal/auth"
	"cc_dashboard/server/internal/model"
	"cc_dashboard/server/internal/security"
	"cc_dashboard/server/internal/store"
)

func (s *Server) handleListNodes(c *gin.Context) {
	nodes, err := s.db.ListNodes()
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	admin := s.isAdmin(c)
	latest, _ := s.db.LatestAll()
	firing, _ := s.db.FiringAlertNodes()
	out := make([]*model.Node, 0, len(nodes))
	for _, n := range nodes {
		// pending 节点仅管理员可见
		if !n.Approved && !admin {
			continue
		}
		if m := latest[n.ID]; m != nil {
			n.Latest = m
		}
		// 展示状态：在线且有阈值告警 → alert
		if n.Status == model.StatusOnline && firing[n.ID] {
			n.Status = model.StatusAlert
		}
		if !admin {
			n.Token = "" // Token 仅管理员可见
		}
		s.attachUpgrade(n)
		out = append(out, n)
	}
	// 可选分页：不传 limit 时返回全部（与旧版行为一致）。
	// 节点多（200+）时客户端可传 ?limit=50&offset=0 分批加载。
	total := len(out)
	limit, offset := 0, 0
	if v, err := strconvParseInt(c.Query("limit")); err == nil && v > 0 {
		limit = int(v)
	}
	if v, err := strconvParseInt(c.Query("offset")); err == nil && v > 0 {
		offset = int(v)
	}
	resp := gin.H{"nodes": out}
	if limit > 0 {
		if offset > total {
			offset = total
		}
		end := offset + limit
		if end > total {
			end = total
		}
		resp["nodes"] = out[offset:end]
		resp["total"] = total
		resp["limit"] = limit
		resp["offset"] = offset
	}
	c.JSON(http.StatusOK, resp)
}

func (s *Server) handleGetNode(c *gin.Context) {
	n, ok := s.nodeByID(c)
	if !ok {
		return
	}
	if m, err := s.db.LatestSample(n.ID); err == nil {
		n.Latest = m
	}
	if !s.isAdmin(c) {
		n.Token = ""
	}
	s.attachUpgrade(n)
	c.JSON(http.StatusOK, gin.H{"node": n})
}

func (s *Server) handleCreateNode(c *gin.Context) {
	var body struct {
		Name           string `json:"name"`
		OSType         string `json:"os_type"`
		AccessMode     string `json:"access_mode"`
		Address        string `json:"address"`
		Port           int    `json:"port"`
		ReportInterval int    `json:"report_interval"`
		Enabled        *bool  `json:"enabled"`
		Direction      string `json:"direction"` // push | pull（模式 A 连接方向）
		GroupTag       string `json:"group_tag"` // 分组标签（自由文本，空=未分组）
		// 模式 B（SSH）/ 模式 C（PVE API）凭据：加密存入 auth_enc
		Username   string `json:"username"`
		Password   string `json:"password"`
		PrivateKey string `json:"private_key"`
		NodeName   string `json:"node_name"` // PVE：节点名（API 路径 /nodes/{node}）
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		fail(c, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		fail(c, http.StatusBadRequest, "name required")
		return
	}
	token, err := security.RandomToken()
	if err != nil {
		fail(c, http.StatusInternalServerError, "token generate failed")
		return
	}
	mode := model.AccessMode(orDefault(body.AccessMode, "agent"))
	n := &model.Node{
		Name:           body.Name,
		OSType:         model.OSType(orDefault(body.OSType, "linux")),
		AccessMode:     mode,
		Address:        body.Address,
		Port:           body.Port,
		Token:          token,
		Enabled:        body.Enabled == nil || *body.Enabled,
		Approved:       true, // 管理员显式创建 = 已批准
		Direction:      body.Direction,
		ReportInterval: body.ReportInterval,
		GroupTag:       strings.TrimSpace(body.GroupTag),
		Status:         model.StatusOffline,
	}
	if mode == model.ModeSSH || mode == model.ModeAPI {
		if mode == model.ModeSSH && n.ReportInterval <= 0 {
			n.ReportInterval = 15 // SSH 模式默认 15s
		}
		if body.Username != "" || body.Password != "" || body.PrivateKey != "" || body.NodeName != "" {
			enc, err := s.encryptCreds(&model.SSHCreds{
				Username: body.Username, Password: body.Password,
				PrivateKey: body.PrivateKey, NodeName: body.NodeName,
			})
			if err != nil {
				fail(c, http.StatusInternalServerError, "encrypt failed")
				return
			}
			n.AuthEnc = enc
		}
	}
	id, err := s.db.CreateNode(n)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	n.ID = id
	s.hub.Broadcast(gin.H{"type": "node_added", "node_id": id})
	c.JSON(http.StatusOK, gin.H{"node": n})
}

// handleReorderNodes 按提交的 id 顺序设置节点排序（管理端上下移）。
// POST /api/v1/nodes/reorder {ids: [3,1,2]}
func (s *Server) handleReorderNodes(c *gin.Context) {
	var body struct {
		IDs []int64 `json:"ids"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		fail(c, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if len(body.IDs) == 0 {
		fail(c, http.StatusBadRequest, "ids required")
		return
	}
	if err := s.db.ReorderNodes(body.IDs); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) handleUpdateNode(c *gin.Context) {
	n, ok := s.nodeByID(c)
	if !ok {
		return
	}
	var body struct {
		Name           *string `json:"name"`
		OSType         *string `json:"os_type"`
		AccessMode     *string `json:"access_mode"`
		Address        *string `json:"address"`
		Port           *int    `json:"port"`
		Enabled        *bool   `json:"enabled"`
		Approved       *bool   `json:"approved"`  // 入网审批：true=批准，false=撤回
		Direction      *string `json:"direction"` // push | pull
		ReportInterval *int    `json:"report_interval"`
		RotateToken    *bool   `json:"rotate_token"`
		GroupTag       *string `json:"group_tag"`    // 分组标签（空串=取消分组）
		MuteMinutes    *int    `json:"mute_minutes"` // 告警静默时长（分钟，0=解除静默）
		// SSH 凭据：任一非空即整体更新（password 传空串清除）
		Username      *string `json:"username"`
		Password      *string `json:"password"`
		PrivateKey    *string `json:"private_key"`
		NodeName      *string `json:"node_name"`
		RotateHostKey *bool   `json:"rotate_host_key"` // 重置 TOFU 主机指纹
		PublicIP      *string `json:"public_ip"`       // 手动指定公网 IP（SSH/API 模式自动获取的兜底）
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		fail(c, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if body.Name != nil && strings.TrimSpace(*body.Name) != "" {
		n.Name = *body.Name
	}
	if body.OSType != nil {
		n.OSType = model.OSType(*body.OSType)
	}
	if body.AccessMode != nil {
		n.AccessMode = model.AccessMode(*body.AccessMode)
	}
	if body.Address != nil {
		n.Address = *body.Address
	}
	if body.Port != nil {
		n.Port = *body.Port
	}
	if body.Enabled != nil {
		n.Enabled = *body.Enabled
	}
	if body.Approved != nil {
		n.Approved = *body.Approved
	}
	if body.Direction != nil {
		n.Direction = *body.Direction
	}
	if body.ReportInterval != nil && *body.ReportInterval > 0 {
		n.ReportInterval = *body.ReportInterval
	}
	if body.GroupTag != nil {
		tag := strings.TrimSpace(*body.GroupTag)
		if r := []rune(tag); len(r) > 24 {
			tag = string(r[:24]) // 按字符截断，避免多字节被劈开
		}
		n.GroupTag = tag
	}
	// 告警静默：维护窗口内仍记录告警，但不发通知
	if body.MuteMinutes != nil {
		m := *body.MuteMinutes
		if m < 0 {
			m = 0
		}
		if m > 7*24*60 {
			m = 7 * 24 * 60 // 最长 7 天，避免误设成永久静默
		}
		if m == 0 {
			n.MuteUntil = 0
		} else {
			n.MuteUntil = time.Now().Unix() + int64(m)*60
		}
	}
	if body.Username != nil || body.Password != nil || body.PrivateKey != nil || body.NodeName != nil {
		creds, err := s.decryptCreds(n)
		if err != nil {
			creds = &model.SSHCreds{}
		}
		if body.Username != nil {
			creds.Username = *body.Username
		}
		if body.Password != nil {
			creds.Password = *body.Password
		}
		if body.PrivateKey != nil {
			creds.PrivateKey = *body.PrivateKey
		}
		if body.NodeName != nil {
			creds.NodeName = *body.NodeName
		}
		enc, err := s.encryptCreds(creds)
		if err != nil {
			fail(c, http.StatusInternalServerError, "encrypt failed")
			return
		}
		n.AuthEnc = enc
	}
	if body.RotateToken != nil && *body.RotateToken {
		token, err := security.RandomToken()
		if err != nil {
			fail(c, http.StatusInternalServerError, "token generate failed")
			return
		}
		n.Token = token
	}
	if body.RotateHostKey != nil && *body.RotateHostKey {
		_ = s.db.ClearNodeHostKey(n.ID)
	}
	if body.PublicIP != nil {
		ip := strings.TrimSpace(*body.PublicIP)
		if ip != "" && net.ParseIP(ip) == nil {
			fail(c, http.StatusBadRequest, "public_ip 不是合法 IP")
			return
		}
		if err := s.db.SetNodePublicIP(n.ID, ip); err != nil {
			fail(c, http.StatusInternalServerError, err.Error())
			return
		}
		n.PublicIP = ip
	}
	if err := s.db.UpdateNode(n); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"node": n})
}

func (s *Server) handleDeleteNode(c *gin.Context) {
	id, err := parseID(c)
	if err != nil {
		return
	}
	if err := s.db.DeleteNode(id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			fail(c, http.StatusNotFound, "node not found")
			return
		}
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	s.engine.Forget(id) // 丢弃内存态计时，避免节点 ID 残留
	s.hub.Broadcast(gin.H{"type": "node_removed", "node_id": id})
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// handleNodeDetail 详情页数据：节点 + 最新实时样本 + 实时明细 + 系统详情 + 硬件清单 + 近期事件 + 告警。
func (s *Server) handleNodeDetail(c *gin.Context) {
	n, ok := s.nodeByID(c)
	if !ok {
		return
	}
	latest, _ := s.db.LatestSample(n.ID)
	detailJSON, _ := s.db.GetNodeDetail(n.ID)
	hwJSON, _ := s.db.GetNodeHardware(n.ID)
	events, _ := s.db.RecentEvents(n.ID, 5)

	var detail any
	if detailJSON != "" {
		_ = jsonUnmarshal([]byte(detailJSON), &detail)
	}
	var hardware any
	if hwJSON != "" {
		_ = jsonUnmarshal([]byte(hwJSON), &hardware)
	}

	firing, _ := s.db.ListAlerts("firing", 200)
	nodeAlerts := make([]*model.Alert, 0)
	for _, a := range firing {
		if a.NodeID == n.ID {
			nodeAlerts = append(nodeAlerts, a)
		}
	}

	if n.Status == model.StatusOnline && len(nodeAlerts) > 0 {
		for _, a := range nodeAlerts {
			if a.Metric != "offline" {
				n.Status = model.StatusAlert
				break
			}
		}
	}
	if !s.isAdmin(c) {
		n.Token = ""
	}
	s.attachUpgrade(n)
	bootEvents, _ := s.db.ListBootEvents(n.ID, 50)

	c.JSON(http.StatusOK, gin.H{
		"node":        n,
		"latest":      latest,
		"live":        json.RawMessage(s.getLive(n.ID)),
		"detail":      detail,
		"boot_events": bootEvents,
		"hardware":    hardware,
		"events":      events,
		"alerts":      nodeAlerts,
	})
}

// handleInstallScript 生成一键安装指令（type=sh|ps1|openwrt，默认 sh）。
func (s *Server) handleInstallScript(c *gin.Context) {
	n, ok := s.nodeByID(c)
	if !ok {
		return
	}
	master := "http://" + c.Request.Host
	typ := c.DefaultQuery("type", "sh")

	var script string
	switch typ {
	case "ps1":
		script = fmt.Sprintf(`# CC_DashBoard Windows 安装（节点: %s）
# 需管理员权限运行
.\install-agent.ps1 -Master %s -Token %s
`, n.Name, master, n.Token)
	case "openwrt":
		script = fmt.Sprintf(`#!/bin/sh
# CC_DashBoard OpenWrt 安装（节点: %s）
# 在路由器上执行（需 root）
uclient-fetch -O - %s/api/v1/nodes/%d/install-script?type=openwrt-body 2>/dev/null | sh -s -- --master=%s --token=%s
# 或手动: 下载 agent-sh/cc-agent.sh 与 init.d 模板后注册 procd
`, n.Name, master, n.ID, master, n.Token)
	default: // sh
		script = fmt.Sprintf(`#!/bin/sh
# CC_DashBoard Linux/NAS 安装指引（节点: %s）
# 1) 编译/获取 Agent 二进制（见 docs/使用部署手册.md 第 4 节）
#    GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o cc-agent ./agent-go
# 2) 上传到目标机器后运行：
./cc-agent -master %s -token %s -interval 5
# 守护：Linux 用 systemd / NAS 用 crontab，OpenWrt 用 agent-sh/init.d 模板
`, n.Name, master, n.Token)
	}

	c.Header("Content-Type", "text/plain; charset=utf-8")
	c.String(http.StatusOK, script)
}

// encryptCreds 序列化并 AES-256-GCM 加密 SSH 凭据。
func (s *Server) encryptCreds(c *model.SSHCreds) (string, error) {
	b, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	enc, err := security.Encrypt(s.key, b)
	if err != nil {
		return "", err
	}
	return string(enc), nil
}

// decryptCreds 解密节点 SSH 凭据；无凭据返回空结构。
func (s *Server) decryptCreds(n *model.Node) (*model.SSHCreds, error) {
	if n.AuthEnc == "" {
		return &model.SSHCreds{}, nil
	}
	plain, err := security.Decrypt(s.key, []byte(n.AuthEnc))
	if err != nil {
		return nil, err
	}
	return model.ParseSSHCreds(plain)
}

// handleRegister 携 Join Key 自注册节点（公开接口）：创建为 pending，后台批准后才允许上报。
// POST /api/v1/register {join_key, name, os_type, address}
func (s *Server) handleRegister(c *gin.Context) {
	var body struct {
		JoinKey string `json:"join_key"`
		Name    string `json:"name"`
		OSType  string `json:"os_type"`
		Address string `json:"address"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		fail(c, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	// Join Key 可选：带了就必须匹配（沿用旧流程）；不带则直接进入待审批，
	// 由管理后台「批准」把关准入（agent 首次启动自动注册即走此路径）。
	if body.JoinKey != "" {
		key, ok, _ := s.db.GetSetting(setJoinKey)
		if !ok || key == "" || body.JoinKey != key {
			fail(c, http.StatusUnauthorized, "join key 错误")
			return
		}
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = "pending-" + auth.RandomHex(2)
	}
	token, err := security.RandomToken()
	if err != nil {
		fail(c, http.StatusInternalServerError, "token generate failed")
		return
	}
	n := &model.Node{
		Name:       name,
		OSType:     model.OSType(orDefault(body.OSType, "linux")),
		AccessMode: model.ModeAgent,
		Address:    body.Address,
		Token:      token,
		Enabled:    true,
		Approved:   false, // 待审批
		Status:     model.StatusOffline,
	}
	id, err := s.db.CreateNode(n)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	n.ID = id
	s.hub.Broadcast(gin.H{"type": "node_pending", "node_id": id})
	c.JSON(http.StatusOK, gin.H{
		"node_id":  id,
		"token":    token,
		"approved": false,
		"message":  "已注册，等待管理员批准后上报才会被接受",
	})
}

func (s *Server) nodeByID(c *gin.Context) (*model.Node, bool) {
	id, err := parseID(c)
	if err != nil {
		return nil, false
	}
	n, err := s.db.GetNode(id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			fail(c, http.StatusNotFound, "node not found")
		} else {
			fail(c, http.StatusInternalServerError, err.Error())
		}
		return nil, false
	}
	return n, true
}

func parseID(c *gin.Context) (int64, error) {
	id, err := strconvParseInt(c.Param("id"))
	if err != nil {
		fail(c, http.StatusBadRequest, "invalid id")
	}
	return id, err
}
