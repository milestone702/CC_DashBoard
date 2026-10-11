// 告警与规则接口。
package api

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"cc_dashboard/server/internal/model"
	"cc_dashboard/server/internal/store"
)

// handleListAlerts ?status=firing|resolved|all（默认 all），按触发时间倒序。
func (s *Server) handleListAlerts(c *gin.Context) {
	status := c.Query("status")
	if status == "all" {
		status = ""
	}
	if status != "" && status != "firing" && status != "resolved" {
		fail(c, http.StatusBadRequest, "status: firing|resolved|all")
		return
	}
	alerts, err := s.db.ListAlerts(status, 200)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	if alerts == nil {
		alerts = []*model.Alert{}
	}
	c.JSON(http.StatusOK, gin.H{"alerts": alerts})
}

func (s *Server) handleListRules(c *gin.Context) {
	rules, err := s.db.ListRules()
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	if rules == nil {
		rules = []*model.AlertRule{}
	}
	c.JSON(http.StatusOK, gin.H{"rules": rules})
}

// ruleBody 创建/更新规则的公共请求体与校验。
type ruleBody struct {
	NodeID    int64    `json:"node_id"`
	Metric    string   `json:"metric"`
	Op        string   `json:"op"`
	Threshold *float64 `json:"threshold"`
	DelaySec  *int     `json:"delay_sec"`
	Enabled   *bool    `json:"enabled"`
}

// validate 校验并填充默认值；返回 (metric, op, threshold, delay, enabled, errMsg)。
func (b *ruleBody) validate() (string, string, float64, int, bool, string) {
	switch b.Metric {
	case "cpu", "mem_pct", "disk_pct":
	default:
		return "", "", 0, 0, false, "metric: cpu|mem_pct|disk_pct"
	}
	if b.Threshold == nil || *b.Threshold < 0 || *b.Threshold > 100 {
		return "", "", 0, 0, false, "threshold 0~100 required"
	}
	op := b.Op
	if op != ">=" {
		op = ">"
	}
	delay := 0
	if b.DelaySec != nil {
		delay = *b.DelaySec
	}
	if delay < 0 || delay > 86400 {
		return "", "", 0, 0, false, "delay_sec 0~86400"
	}
	enabled := b.Enabled == nil || *b.Enabled
	return b.Metric, op, *b.Threshold, delay, enabled, ""
}

// handleCreateRule body: {node_id:0=全局, metric:cpu|mem_pct|disk_pct, op:>/>=, threshold, delay_sec, enabled}
// delay_sec：连续超阈值 N 秒后才触发（如 5 分钟 = 300）；0=立即。
func (s *Server) handleCreateRule(c *gin.Context) {
	var body ruleBody
	if err := c.ShouldBindJSON(&body); err != nil {
		fail(c, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	metric, op, threshold, delay, enabled, errMsg := body.validate()
	if errMsg != "" {
		fail(c, http.StatusBadRequest, errMsg)
		return
	}
	r := &model.AlertRule{
		NodeID:    body.NodeID,
		Metric:    metric,
		Op:        op,
		Threshold: threshold,
		DelaySec:  delay,
		Enabled:   enabled,
	}
	id, err := s.db.CreateRule(r)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	r.ID = id
	c.JSON(http.StatusOK, gin.H{"rule": r})
}

// handleUpdateRule PUT /alerts/rules/:id —— 规则编辑。
func (s *Server) handleUpdateRule(c *gin.Context) {
	id, err := strconvParseInt(c.Param("id"))
	if err != nil {
		fail(c, http.StatusBadRequest, "invalid id")
		return
	}
	var body ruleBody
	if err := c.ShouldBindJSON(&body); err != nil {
		fail(c, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	metric, op, threshold, delay, enabled, errMsg := body.validate()
	if errMsg != "" {
		fail(c, http.StatusBadRequest, errMsg)
		return
	}
	r := &model.AlertRule{
		ID:        id,
		NodeID:    body.NodeID,
		Metric:    metric,
		Op:        op,
		Threshold: threshold,
		DelaySec:  delay,
		Enabled:   enabled,
	}
	if err := s.db.UpdateRule(r); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			fail(c, http.StatusNotFound, "rule not found")
			return
		}
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"rule": r})
}

func (s *Server) handleDeleteRule(c *gin.Context) {
	id, err := strconvParseInt(c.Param("id"))
	if err != nil {
		fail(c, http.StatusBadRequest, "invalid id")
		return
	}
	if err := s.db.DeleteRule(id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			fail(c, http.StatusNotFound, "rule not found")
			return
		}
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
