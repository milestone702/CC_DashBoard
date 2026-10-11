// 通知配置接口（管理员）。
package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"cc_dashboard/server/internal/notify"
	"cc_dashboard/server/internal/store"
)

// GET /api/v1/notify/config — token/pass 不回传明文，仅标记是否已配置。
func (s *Server) handleGetNotifyConfig(c *gin.Context) {
	cfg := notify.Load(s.db)
	c.JSON(http.StatusOK, gin.H{
		"enabled":   cfg.Enabled,
		"channel":   cfg.Channel,
		"token_set": cfg.Token != "",
		"url_set":   cfg.URL != "",
		"smtp_host": cfg.SMTPHost,
		"smtp_port": cfg.SMTPPort,
		"smtp_user": cfg.SMTPUser,
		"smtp_from": cfg.SMTPFrom,
		"smtp_to":   cfg.SMTPTo,
		"smtp_pass_set": cfg.SMTPPass != "",
	})
}

// PUT /api/v1/notify/config — token/url/smtp_pass 传空字符串表示保持不变。
func (s *Server) handlePutNotifyConfig(c *gin.Context) {
	var body struct {
		Enabled  bool   `json:"enabled"`
		Channel  string `json:"channel"`
		Token    string `json:"token"`
		URL      string `json:"url"`
		SMTPHost string `json:"smtp_host"`
		SMTPPort *int   `json:"smtp_port"`
		SMTPUser string `json:"smtp_user"`
		SMTPPass string `json:"smtp_pass"`
		SMTPFrom string `json:"smtp_from"`
		SMTPTo   string `json:"smtp_to"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		fail(c, http.StatusBadRequest, "invalid json")
		return
	}
	switch body.Channel {
	case "pushplus", "webhook", "feishu", "dingtalk", "wecom", "smtp":
	default:
		fail(c, http.StatusBadRequest, "channel: pushplus|webhook|feishu|dingtalk|wecom|smtp")
		return
	}
	cfg := notify.Load(s.db)
	cfg.Enabled = body.Enabled
	cfg.Channel = body.Channel
	if body.Token != "" {
		cfg.Token = body.Token
	}
	if body.URL != "" {
		cfg.URL = body.URL
	}
	// SMTP 字段：非空即更新（smtp_pass 空=保持；port 指针为 nil=保持）
	if body.SMTPHost != "" {
		cfg.SMTPHost = body.SMTPHost
	}
	if body.SMTPPort != nil {
		cfg.SMTPPort = *body.SMTPPort
	}
	if body.SMTPUser != "" {
		cfg.SMTPUser = body.SMTPUser
	}
	if body.SMTPPass != "" {
		cfg.SMTPPass = body.SMTPPass
	}
	if body.SMTPFrom != "" {
		cfg.SMTPFrom = body.SMTPFrom
	}
	if body.SMTPTo != "" {
		cfg.SMTPTo = body.SMTPTo
	}
	if err := notify.Save(s.db, cfg); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// POST /api/v1/notify/test — 无视启用开关，强制发送一条测试消息（结果落库）。
func (s *Server) handleNotifyTest(c *gin.Context) {
	cfg := notify.Load(s.db)
	const title = "CC_DashBoard 测试通知"
	err := notify.Send(cfg, title, "看到这条消息说明通知渠道配置成功。")
	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	}
	_ = s.db.InsertNotifyLog(cfg.Channel, title, err == nil, errMsg)
	if err != nil {
		fail(c, http.StatusBadGateway, "发送失败: "+errMsg)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// GET /api/v1/notify/logs — 最近发送记录（管理员）。
func (s *Server) handleListNotifyLogs(c *gin.Context) {
	logs, err := s.db.ListNotifyLogs(50)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	if logs == nil {
		logs = []*store.NotifyLog{}
	}
	c.JSON(http.StatusOK, gin.H{"logs": logs})
}
