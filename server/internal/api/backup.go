// 配置导出：把「可重建的配置」打包成 JSON，便于换机 / 重装后快速恢复。
// 只导出非敏感配置：**不含**节点 Token、SSH/PVE 凭据、API Key 哈希、各类密码与通知密钥。
package api

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"cc_dashboard/server/internal/model"
	"cc_dashboard/server/internal/security"
)

// backupPayload 导出结构（version 用于后续兼容演进）。
type backupPayload struct {
	Version    int               `json:"version"`
	ExportedAt int64             `json:"exported_at"`
	ServerVer  string            `json:"server_version,omitempty"`
	Site       map[string]string `json:"site"`
	Settings   map[string]string `json:"settings"`
	Nodes      []backupNode      `json:"nodes"`
	AlertRules []backupRule      `json:"alert_rules"`
}

// backupRule 规则快照：额外带节点名，导入时用它把旧 node_id 映射到新库的 ID。
type backupRule struct {
	NodeName string `json:"node_name,omitempty"` // 空=全局规则
	model.AlertRule
}

// backupNode 节点可导出字段（刻意剔除 token / auth_enc / 运行态）。
type backupNode struct {
	Name           string `json:"name"`
	OSType         string `json:"os_type"`
	AccessMode     string `json:"access_mode"`
	Address        string `json:"address"`
	Port           int    `json:"port"`
	Direction      string `json:"direction"`
	ReportInterval int    `json:"report_interval"`
	Enabled        bool   `json:"enabled"`
	GroupTag       string `json:"group_tag"`
	PublicIP       string `json:"public_ip"`
}

// backupSettingKeys 允许导出的 settings 键（全部为展示类配置）。
var backupSettingKeys = []string{
	"site_title", "overview_cols", "view_password_enabled",
}

// GET /api/v1/backup — 下载配置快照（admin）
func (s *Server) handleBackupExport(c *gin.Context) {
	nodes, err := s.db.ListNodes()
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	rules, err := s.db.ListRules()
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}

	out := backupPayload{
		Version:    1,
		ExportedAt: time.Now().Unix(),
		ServerVer:  Version,
		Site:       map[string]string{},
		Settings:   map[string]string{},
		Nodes:      make([]backupNode, 0, len(nodes)),
		AlertRules: make([]backupRule, 0, len(rules)),
	}
	// 节点 ID → 名称，供规则快照带上 node_name（导入时按名字重新映射）
	nodeName := make(map[int64]string, len(nodes))
	for _, n := range nodes {
		nodeName[n.ID] = n.Name
	}
	for _, r := range rules {
		out.AlertRules = append(out.AlertRules, backupRule{NodeName: nodeName[r.NodeID], AlertRule: *r})
	}
	if t, ok, _ := s.db.GetSetting("site_title"); ok {
		out.Site["title"] = t
	}
	for _, k := range backupSettingKeys {
		if v, ok, _ := s.db.GetSetting(k); ok && v != "" {
			out.Settings[k] = v
		}
	}
	for _, n := range nodes {
		out.Nodes = append(out.Nodes, backupNode{
			Name: n.Name, OSType: string(n.OSType), AccessMode: string(n.AccessMode),
			Address: n.Address, Port: n.Port, Direction: n.Direction,
			ReportInterval: n.ReportInterval, Enabled: n.Enabled,
			GroupTag: n.GroupTag, PublicIP: n.PublicIP,
		})
	}

	name := "cc-dashboard-backup-" + time.Now().Format("20060102-1504") + ".json"
	c.Header("Content-Disposition", `attachment; filename="`+name+`"`)
	c.JSON(http.StatusOK, out)
}

// POST /api/v1/restore — 从备份快照恢复配置（admin）。
// 安全策略：**只新增、不覆盖**。同名节点 / 同参数规则一律跳过，避免误删现有配置。
// 导入的 Agent 节点会生成**新的上报 Token**（旧 Token 不可能也不应该恢复），
// SSH/PVE/群晖凭据不包含在备份中，需导入后手工补填。
func (s *Server) handleRestore(c *gin.Context) {
	var p backupPayload
	if err := c.ShouldBindJSON(&p); err != nil {
		fail(c, http.StatusBadRequest, "invalid backup json: "+err.Error())
		return
	}
	if p.Version != 1 {
		fail(c, http.StatusBadRequest, "unsupported backup version")
		return
	}

	existing, err := s.db.ListNodes()
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	byName := make(map[string]int64, len(existing))
	for _, n := range existing {
		byName[n.Name] = n.ID
	}

	nodesCreated, nodesSkipped := 0, 0
	for _, bn := range p.Nodes {
		if _, ok := byName[bn.Name]; ok {
			nodesSkipped++
			continue
		}
		token, terr := security.RandomToken()
		if terr != nil {
			fail(c, http.StatusInternalServerError, "token generate failed")
			return
		}
		node := &model.Node{
			Name: bn.Name, OSType: model.OSType(orDefault(bn.OSType, "linux")),
			AccessMode: model.AccessMode(orDefault(bn.AccessMode, "agent")),
			Address:    bn.Address, Port: bn.Port, Direction: bn.Direction,
			ReportInterval: bn.ReportInterval, Enabled: bn.Enabled,
			GroupTag: bn.GroupTag, PublicIP: bn.PublicIP,
			Token: token, Approved: true, Status: model.StatusOffline,
		}
		id, cerr := s.db.CreateNode(node)
		if cerr != nil {
			nodesSkipped++
			continue
		}
		byName[bn.Name] = id
		nodesCreated++
	}

	existingRules, _ := s.db.ListRules()
	type ruleKey struct {
		nodeID    int64
		metric    string
		op        string
		threshold float64
	}
	seen := make(map[ruleKey]bool, len(existingRules))
	for _, r := range existingRules {
		seen[ruleKey{r.NodeID, r.Metric, r.Op, r.Threshold}] = true
	}

	rulesCreated, rulesSkipped := 0, 0
	for _, br := range p.AlertRules {
		nodeID := int64(0) // 全局
		if br.NodeName != "" {
			id, ok := byName[br.NodeName]
			if !ok {
				rulesSkipped++ // 目标节点不存在（既没导入也没现成同名节点）
				continue
			}
			nodeID = id
		}
		k := ruleKey{nodeID, br.Metric, br.Op, br.Threshold}
		if seen[k] {
			rulesSkipped++
			continue
		}
		if _, cerr := s.db.CreateRule(&model.AlertRule{
			NodeID: nodeID, Metric: br.Metric, Op: br.Op,
			Threshold: br.Threshold, DelaySec: br.DelaySec, Enabled: br.Enabled,
		}); cerr != nil {
			rulesSkipped++
			continue
		}
		seen[k] = true
		rulesCreated++
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":            true,
		"nodes_created": nodesCreated, "nodes_skipped": nodesSkipped,
		"rules_created": rulesCreated, "rules_skipped": rulesSkipped,
	})
}
