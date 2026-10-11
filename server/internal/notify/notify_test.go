package notify

import (	"bufio"
	"encoding/base64"
	"fmt"
	"net"
	"time"

	"encoding/json"
	"strings"
	"testing"
)

func TestPushPlusBody(t *testing.T) {
	var m map[string]string
	if err := json.Unmarshal(pushPlusBody("tk", "标题", "内容"), &m); err != nil {
		t.Fatal(err)
	}
	if m["token"] != "tk" || m["title"] != "标题" || m["content"] != "内容" {
		t.Errorf("pushplus body = %v", m)
	}
}

func TestFeishuBody(t *testing.T) {
	var m struct {
		MsgType string `json:"msg_type"`
		Content struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(feishuBody("告警触发 · 主机", "CPU 95%"), &m); err != nil {
		t.Fatal(err)
	}
	if m.MsgType != "text" {
		t.Errorf("msg_type = %s", m.MsgType)
	}
	if !strings.Contains(m.Content.Text, "告警触发") || !strings.Contains(m.Content.Text, "CPU 95%") {
		t.Errorf("text = %q", m.Content.Text)
	}
}

func TestDingtalkBody(t *testing.T) {
	var m struct {
		MsgType string `json:"msgtype"`
		Text    struct {
			Content string `json:"content"`
		} `json:"text"`
	}
	if err := json.Unmarshal(dingtalkBody("T", "C"), &m); err != nil {
		t.Fatal(err)
	}
	if m.MsgType != "text" || !strings.Contains(m.Text.Content, "T\nC") {
		t.Errorf("dingtalk body = %+v", m)
	}
}

func TestWebhookBody(t *testing.T) {
	var m map[string]string
	if err := json.Unmarshal(webhookBody("t", "c"), &m); err != nil {
		t.Fatal(err)
	}
	if m["source"] != "CC_DashBoard" {
		t.Errorf("source = %s", m["source"])
	}
}

// ---------- SMTP ----------

func TestBuildMail(t *testing.T) {
	msg := string(buildMail("from@x.com", []string{"a@x.com", "b@x.com"}, "告警触发 · NAS", "CPU 95%"))
	for _, want := range []string{
		"From: from@x.com",
		"To: a@x.com, b@x.com",
		"Subject: =?UTF-8?B?", // RFC2047 中文主题
		"Content-Type: text/plain; charset=utf-8",
		"Content-Transfer-Encoding: base64",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("mail 缺少 %q:\n%s", want, msg)
		}
	}
	// 正文 base64 可解
	parts := strings.SplitN(msg, "\r\n\r\n", 2)
	if len(parts) != 2 {
		t.Fatalf("无正文分隔: %s", msg)
	}
	dec, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(strings.TrimSpace(parts[1]), "\r\n", ""))
	if err != nil || string(dec) != "CPU 95%" {
		t.Errorf("body decode = %q err=%v", dec, err)
	}
}

func TestSplitEmails(t *testing.T) {
	got := splitEmails(" a@x.com, b@y.com;c@z.com ")
	if len(got) != 3 || got[0] != "a@x.com" || got[2] != "c@z.com" {
		t.Errorf("split = %v", got)
	}
	if len(splitEmails("")) != 0 {
		t.Error("空串应为空列表")
	}
}

// startFakeSMTP 极简 SMTP 服务器（明文、无认证），返回监听端口与收信通道。
func startFakeSMTP(t *testing.T) (int, chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	recv := make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		fmt.Fprint(conn, "220 fake ESMTP ready\r\n")
		sc := bufio.NewScanner(conn)
		inData := false
		var data strings.Builder
		for sc.Scan() {
			line := sc.Text()
			if inData {
				if line == "." {
					inData = false
					fmt.Fprint(conn, "250 OK queued\r\n")
					recv <- data.String()
					continue
				}
				data.WriteString(line)
				data.WriteString("\n")
				continue
			}
			up := strings.ToUpper(line)
			switch {
			case strings.HasPrefix(up, "EHLO"), strings.HasPrefix(up, "HELO"):
				fmt.Fprint(conn, "250-fake\r\n250 8BITMIME\r\n")
			case strings.HasPrefix(up, "MAIL FROM"), strings.HasPrefix(up, "RCPT TO"):
				fmt.Fprint(conn, "250 OK\r\n")
			case strings.HasPrefix(up, "DATA"):
				inData = true
				fmt.Fprint(conn, "354 send it\r\n")
			case strings.HasPrefix(up, "QUIT"):
				fmt.Fprint(conn, "221 bye\r\n")
				return
			default:
				fmt.Fprint(conn, "250 OK\r\n")
			}
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, recv
}

func TestSendSMTP(t *testing.T) {
	port, recv := startFakeSMTP(t)
	cfg := Config{
		SMTPHost: "127.0.0.1", SMTPPort: port,
		SMTPFrom: "alerts@example.com", SMTPTo: "ops@example.com",
	}
	if err := sendSMTP(cfg, "节点离线", "NAS 超过 60 秒未上报"); err != nil {
		t.Fatalf("sendSMTP: %v", err)
	}
	select {
	case mail := <-recv:
		if !strings.Contains(mail, "From: alerts@example.com") {
			t.Errorf("mail from: %s", mail)
		}
		if !strings.Contains(mail, "Subject: =?UTF-8?B?") {
			t.Errorf("subject 未编码: %s", mail)
		}
		if !strings.Contains(mail, "base64") {
			t.Errorf("body 未 base64: %s", mail)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("fake SMTP 未收到邮件")
	}
}

func TestSendSMTPErrors(t *testing.T) {
	// 缺主机
	if err := sendSMTP(Config{SMTPTo: "a@b.c"}, "t", "c"); err == nil {
		t.Error("空 host 应报错")
	}
	// 缺收件人
	if err := sendSMTP(Config{SMTPHost: "x"}, "t", "c"); err == nil {
		t.Error("空 to 应报错")
	}
	// 缺发件人（无 from 且无 user）
	if err := sendSMTP(Config{SMTPHost: "x", SMTPTo: "a@b.c"}, "t", "c"); err == nil {
		t.Error("空 from 应报错")
	}
}
