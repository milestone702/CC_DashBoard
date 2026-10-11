// 首次启动自动注册：配置只填 master 即可，服务端生成节点 token，
// 是否准入由管理后台「批准」（pending 状态上报返回 403）。
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"
)

// tokenPlaceholder 配置模板占位符：视同未填写，触发自动注册。
const tokenPlaceholder = "REPLACE_WITH_NODE_TOKEN"

// registerNode 向 Master 自注册，返回节点 token 与是否已批准。
func registerNode(master string) (string, bool, error) {
	name, _ := os.Hostname()
	payload := map[string]string{
		"name":    name,
		"os_type": detectOSType(),
		"address": primaryIP(),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", false, err
	}
	req, err := http.NewRequest(http.MethodPost,
		strings.TrimRight(master, "/")+"/api/v1/register", bytes.NewReader(body))
	if err != nil {
		return "", false, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &e)
		if e.Message != "" {
			return "", false, fmt.Errorf("服务端拒绝 %d: %s", resp.StatusCode, e.Message)
		}
		return "", false, fmt.Errorf("服务端拒绝 %d", resp.StatusCode)
	}
	var r struct {
		Token    string `json:"token"`
		Approved bool   `json:"approved"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return "", false, err
	}
	if r.Token == "" {
		return "", false, errors.New("服务端未返回 token")
	}
	return r.Token, r.Approved, nil
}

// saveToken 把 token 写回配置文件，保留其他字段与文件权限。
func saveToken(path, token string) error {
	raw := map[string]json.RawMessage{}
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &raw); err != nil {
			return fmt.Errorf("解析配置失败: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	tok, _ := json.Marshal(token)
	raw["token"] = tok
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	mode := os.FileMode(0o600)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// detectOSType 面板展示用：OpenWrt 单独识别，其余按 GOOS。
func detectOSType() string {
	if _, err := os.Stat("/etc/openwrt_release"); err == nil {
		return "openwrt"
	}
	return runtime.GOOS
}

// primaryIP 取首个非回环 IPv4，供面板地址列展示。
func primaryIP() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, ia := range ifaces {
		if ia.Flags&net.FlagUp == 0 || ia.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ia.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok || ipnet.IP.IsLoopback() || ipnet.IP.To4() == nil {
				continue
			}
			return ipnet.IP.String()
		}
	}
	return ""
}
