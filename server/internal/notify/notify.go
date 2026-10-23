// 告警通知渠道：pushplus / 通用 Webhook / 飞书机器人 / 钉钉机器人。
// 发送失败自动重试（最多 3 次：立即、+1s、+3s）。
package notify

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"cc_dashboard/server/internal/store"
)

// settings 键名
const (
	KeyEnabled = "notify_enabled"
	KeyChannel = "notify_channel" // pushplus | webhook | feishu | dingtalk | wecom | smtp
	KeyToken   = "notify_token"   // pushplus token
	KeyURL     = "notify_url"     // webhook / 飞书 / 钉钉地址
	// SMTP 邮件渠道
	KeySMTPHost = "smtp_host"
	KeySMTPPort = "smtp_port"
	KeySMTPUser = "smtp_user"
	KeySMTPPass = "smtp_pass"
	KeySMTPFrom = "smtp_from"
	KeySMTPTo   = "smtp_to"
)

// Config 通知配置。
type Config struct {
	Enabled bool   `json:"enabled"`
	Channel string `json:"channel"`
	Token   string `json:"token,omitempty"`
	URL     string `json:"url,omitempty"`
	// SMTP（Channel=smtp 时生效）
	SMTPHost string `json:"smtp_host,omitempty"`
	SMTPPort int    `json:"smtp_port,omitempty"` // 默认 465（隐式 TLS）
	SMTPUser string `json:"smtp_user,omitempty"`
	SMTPPass string `json:"smtp_pass,omitempty"`
	SMTPFrom string `json:"smtp_from,omitempty"` // 空则用 SMTPUser
	SMTPTo   string `json:"smtp_to,omitempty"`   // 逗号分隔多个收件人
}

var httpClient = &http.Client{Timeout: 6 * time.Second}

// Load 读取配置。
func Load(db *store.DB) Config {
	enabled, _, _ := db.GetSetting(KeyEnabled)
	channel, _, _ := db.GetSetting(KeyChannel)
	token, _, _ := db.GetSetting(KeyToken)
	url, _, _ := db.GetSetting(KeyURL)
	if channel == "" {
		channel = "pushplus"
	}
	host, _, _ := db.GetSetting(KeySMTPHost)
	portStr, _, _ := db.GetSetting(KeySMTPPort)
	user, _, _ := db.GetSetting(KeySMTPUser)
	pass, _, _ := db.GetSetting(KeySMTPPass)
	from, _, _ := db.GetSetting(KeySMTPFrom)
	to, _, _ := db.GetSetting(KeySMTPTo)
	port, _ := strconv.Atoi(portStr)
	return Config{
		Enabled: enabled == "1", Channel: channel, Token: token, URL: url,
		SMTPHost: host, SMTPPort: port, SMTPUser: user, SMTPPass: pass,
		SMTPFrom: from, SMTPTo: to,
	}
}

// Save 保存配置。
func Save(db *store.DB, cfg Config) error {
	on := "0"
	if cfg.Enabled {
		on = "1"
	}
	if err := db.SetSetting(KeyEnabled, on); err != nil {
		return err
	}
	if err := db.SetSetting(KeyChannel, cfg.Channel); err != nil {
		return err
	}
	if err := db.SetSetting(KeyToken, cfg.Token); err != nil {
		return err
	}
	if err := db.SetSetting(KeyURL, cfg.URL); err != nil {
		return err
	}
	if err := db.SetSetting(KeySMTPHost, cfg.SMTPHost); err != nil {
		return err
	}
	if err := db.SetSetting(KeySMTPPort, strconv.Itoa(cfg.SMTPPort)); err != nil {
		return err
	}
	if err := db.SetSetting(KeySMTPUser, cfg.SMTPUser); err != nil {
		return err
	}
	if err := db.SetSetting(KeySMTPPass, cfg.SMTPPass); err != nil {
		return err
	}
	if err := db.SetSetting(KeySMTPFrom, cfg.SMTPFrom); err != nil {
		return err
	}
	return db.SetSetting(KeySMTPTo, cfg.SMTPTo)
}

// Fire 告警引擎调用：配置启用时异步发送（不阻塞采集链路）。
// 成功/失败均落库 notify_logs（重试耗尽保留最后错误）。
func Fire(db *store.DB, title, content string) {
	cfg := Load(db)
	if !cfg.Enabled {
		return
	}
	go func() {
		err := Send(cfg, title, content)
		if err != nil {
			log.Printf("通知发送失败(%s): %v", cfg.Channel, err)
		}
		if dbErr := db.InsertNotifyLog(cfg.Channel, title, err == nil, errString(err)); dbErr != nil {
			log.Printf("通知记录写入失败: %v", dbErr)
		}
	}()
}

// errString nil → 空串。
func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// Send 按渠道发送并重试（最多 3 次），忽略 Enabled（供测试按钮使用）。
func Send(cfg Config, title, content string) error {
	var last error
	for attempt := 1; attempt <= 3; attempt++ {
		last = sendOnce(cfg, title, content)
		if last == nil {
			return nil
		}
		if attempt < 3 {
			// 1s、3s 退避
			time.Sleep(time.Duration(attempt*2-1) * time.Second)
		}
	}
	return fmt.Errorf("重试 3 次仍失败: %w", last)
}

func sendOnce(cfg Config, title, content string) error {
	switch cfg.Channel {
	case "webhook":
		return postJSON(cfg.URL, webhookBody(title, content))
	case "feishu":
		return postJSON(cfg.URL, feishuBody(title, content))
	case "dingtalk":
		return postJSON(cfg.URL, dingtalkBody(title, content))
	case "wecom":
		return postJSON(cfg.URL, wecomBody(title, content))
	case "smtp":
		return sendSMTP(cfg, title, content)
	case "ntfy":
		return sendNtfy(cfg, title, content)
	case "telegram":
		return sendTelegram(cfg, title, content)
	default: // pushplus
		if cfg.Token == "" {
			return errors.New("pushplus token 未配置")
		}
		return postJSON("https://www.pushplus.plus/send", pushPlusBody(cfg.Token, title, content))
	}
}

// sendNtfy 推送到 ntfy 主题（https://ntfy.sh/<topic> 或自建实例）。
// 手机装 ntfy App 订阅同一主题即可收到系统通知，无需自建推送服务。
// URL = 完整主题地址；Token = 可选访问令牌（受保护主题）。
func sendNtfy(cfg Config, title, content string) error {
	if cfg.URL == "" {
		return errors.New("ntfy 主题地址未配置（如 https://ntfy.sh/my-topic）")
	}
	body := title + "\n\n" + content
	req, err := http.NewRequest(http.MethodPost, cfg.URL, strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	req.Header.Set("Title", title) // 通知栏标题；非 ASCII 由 ntfy 按 UTF-8 处理
	req.Header.Set("Priority", "default")
	if cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.Token)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("ntfy 返回 %d", resp.StatusCode)
	}
	return nil
}

// sendTelegram 通过 Bot API 发送（Token=bot token，URL=chat_id）。
// chat_id 形如 123456789，群组为 -1001234567890。
func sendTelegram(cfg Config, title, content string) error {
	if cfg.Token == "" || cfg.URL == "" {
		return errors.New("telegram 需要 bot Token 与 chat_id")
	}
	body, _ := json.Marshal(map[string]any{
		"chat_id":                  cfg.URL,
		"text":                     title + "\n\n" + content,
		"disable_web_page_preview": true,
	})
	return postJSON("https://api.telegram.org/bot"+cfg.Token+"/sendMessage", body)
}

// ---------- 各渠道消息体（纯函数，便于单元测试） ----------

func pushPlusBody(token, title, content string) []byte {
	b, _ := json.Marshal(map[string]string{
		"token": token, "title": title, "content": content,
	})
	return b
}

func webhookBody(title, content string) []byte {
	b, _ := json.Marshal(map[string]string{
		"title": title, "content": content, "source": "CC_DashBoard",
	})
	return b
}

// feishuBody 飞书自定义机器人 text 消息。
func feishuBody(title, content string) []byte {
	b, _ := json.Marshal(map[string]any{
		"msg_type": "text",
		"content":  map[string]string{"text": title + "\n" + content},
	})
	return b
}

// dingtalkBody 钉钉自定义机器人 text 消息（加 key 到 URL query 实现安全设置）。
func dingtalkBody(title, content string) []byte {
	b, _ := json.Marshal(map[string]any{
		"msgtype": "text",
		"text":    map[string]string{"content": title + "\n" + content},
	})
	return b
}

// wecomBody 企业微信群机器人 text 消息。
func wecomBody(title, content string) []byte {
	b, _ := json.Marshal(map[string]any{
		"msgtype": "text",
		"text":    map[string]string{"content": title + "\n" + content},
	})
	return b
}

// sendSMTP SMTP 邮件发送：465 隐式 TLS（smtps）；25/587 明文连接（587 支持时 STARTTLS）。
// 认证：PLAIN（须在 TLS 连接上，Go net/smtp 强制）。收件人逗号分隔。
func sendSMTP(cfg Config, title, content string) error {
	if cfg.SMTPHost == "" {
		return errors.New("SMTP 主机未配置")
	}
	toList := splitEmails(cfg.SMTPTo)
	if len(toList) == 0 {
		return errors.New("SMTP 收件人未配置")
	}
	port := cfg.SMTPPort
	if port <= 0 {
		port = 465
	}
	from := cfg.SMTPFrom
	if from == "" {
		from = cfg.SMTPUser
	}
	if from == "" {
		return errors.New("SMTP 发件人未配置（smtp_from 或用户名）")
	}
	addr := net.JoinHostPort(cfg.SMTPHost, strconv.Itoa(port))
	dialer := &net.Dialer{Timeout: 8 * time.Second}

	var conn net.Conn
	var err error
	if port == 465 {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{ServerName: cfg.SMTPHost})
	} else {
		conn, err = dialer.Dial("tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("SMTP 连接失败: %w", err)
	}
	defer conn.Close()

	c, err := smtp.NewClient(conn, cfg.SMTPHost)
	if err != nil {
		return fmt.Errorf("SMTP 客户端初始化失败: %w", err)
	}
	defer c.Close()

	// 非 465：能 STARTTLS 则升级（587 常见）
	if port != 465 {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(&tls.Config{ServerName: cfg.SMTPHost}); err != nil {
				return fmt.Errorf("STARTTLS 失败: %w", err)
			}
		}
	}
	if cfg.SMTPUser != "" {
		if ok, _ := c.Extension("AUTH"); ok {
			auth := smtp.PlainAuth("", cfg.SMTPUser, cfg.SMTPPass, cfg.SMTPHost)
			if err := c.Auth(auth); err != nil {
				return fmt.Errorf("SMTP 认证失败: %w", err)
			}
		}
	}
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("MAIL FROM 失败: %w", err)
	}
	for _, to := range toList {
		if err := c.Rcpt(to); err != nil {
			return fmt.Errorf("RCPT TO %s 失败: %w", to, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("DATA 失败: %w", err)
	}
	if _, err := w.Write(buildMail(from, toList, title, content)); err != nil {
		return fmt.Errorf("正文写入失败: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("DATA 结束失败: %w", err)
	}
	return c.Quit()
}

// splitEmails 逗号/分号分隔的邮箱列表 → 规范化切片。
func splitEmails(s string) []string {
	var out []string
	for _, e := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' }) {
		e = strings.TrimSpace(e)
		if e != "" {
			out = append(out, e)
		}
	}
	return out
}

// buildMail 构造 RFC 5322 纯文本邮件：主题 RFC 2047 编码、正文 base64（76 列折行）。
func buildMail(from string, to []string, subject, body string) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", strings.Join(to, ", "))
	fmt.Fprintf(&b, "Subject: %s\r\n", encodeSubject(subject))
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
	enc := base64.StdEncoding.EncodeToString([]byte(body))
	for i := 0; i < len(enc); i += 76 {
		end := i + 76
		if end > len(enc) {
			end = len(enc)
		}
		b.WriteString(enc[i:end])
		b.WriteString("\r\n")
	}
	return b.Bytes()
}

// encodeSubject RFC 2047 主题编码（=?UTF-8?B?...?=；纯 ASCII 原样）。
// 不用 mime.BEncoding：其输出小写 token（=?utf-8?b?），部分老旧客户端不认。
func encodeSubject(s string) string {
	ascii := true
	for _, r := range s {
		if r > 127 {
			ascii = false
			break
		}
	}
	if ascii {
		return s
	}
	return "=?UTF-8?B?" + base64.StdEncoding.EncodeToString([]byte(s)) + "?="
}

func postJSON(url string, body []byte) error {
	if url == "" {
		return errors.New("通知 URL 未配置")
	}
	resp, err := httpClient.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}
