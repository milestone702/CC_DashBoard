// cc-agent --install 交互式安装：写入配置文件并注册系统服务。
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// agentConfig 服务模式运行配置（--config 加载）。
type agentConfig struct {
	Master       string `json:"master"`
	Token        string `json:"token"`
	IntervalSec  int    `json:"interval_sec"`
	Mode         string `json:"mode"`
	Listen       string `json:"listen"`
}

func defaultConfigPath() string {
	exe, err := os.Executable()
	if err != nil {
		return "cc-agent.config.json"
	}
	return filepath.Join(filepath.Dir(exe), "cc-agent.config.json")
}

func loadConfig(path string) (*agentConfig, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c agentConfig
	return &c, json.Unmarshal(b, &c)
}

// runInstall 交互式问答 → 保存配置 → 注册服务。
func runInstall() {
	fmt.Println("=== CC_DashBoard Agent 安装向导 ===")
	in := bufio.NewReader(os.Stdin)
	ask := func(label, def string) string {
		if def != "" {
			fmt.Printf("%s [%s]: ", label, def)
		} else {
			fmt.Printf("%s: ", label)
		}
		line, _ := in.ReadString('\n')
		line = strings.TrimSpace(line)
		if line == "" {
			return def
		}
		return line
	}

	cfg := agentConfig{
		Master:      ask("Master 地址 (http://host:8080)", "http://127.0.0.1:8080"),
		Token:       ask("节点 Token", ""),
		Mode:        ask("连接模式 push(反向)/listen(正向)", "push"),
		Listen:      ask("正向监听地址 (-mode=listen 生效)", ":9100"),
		IntervalSec: 5,
	}
	if iv := ask("上报周期(秒)", "5"); iv != "" {
		if n, err := strconv.Atoi(iv); err == nil && n > 0 {
			cfg.IntervalSec = n
		}
	}
	if cfg.Token == "" {
		fmt.Println("✓ Token 留空：首次启动将自动向 Master 注册（管理后台批准后生效）")
	}

	// 写配置
	path := defaultConfigPath()
	b, _ := json.MarshalIndent(&cfg, "", "  ")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		fmt.Printf("✗ 写配置失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ 配置已写入 %s\n", path)

	if err := registerService(path); err != nil {
		fmt.Printf("⚠ 自动注册服务失败: %v\n", err)
		printManualService(path)
		return
	}
	fmt.Println("✓ 服务已注册并启动（开机自启）")
}

func registerService(cfgPath string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	switch runtime.GOOS {
	case "windows":
		bin := fmt.Sprintf(`"%s" --config "%s"`, exe, cfgPath)
		out, err := exec.Command("sc.exe", "create", "cc-agent",
			"binPath=", bin, "start=", "auto",
			"DisplayName=", "CC_DashBoard Agent").CombinedOutput()
		if err != nil {
			// 已存在则更新 binPath 后启动
			exec.Command("sc.exe", "delete", "cc-agent").Run()
			out, err = exec.Command("sc.exe", "create", "cc-agent",
				"binPath=", bin, "start=", "auto",
				"DisplayName=", "CC_DashBoard Agent").CombinedOutput()
			if err != nil {
				return fmt.Errorf("%s", strings.TrimSpace(string(out)))
			}
		}
		_ = exec.Command("sc.exe", "start", "cc-agent").Run()
		return nil
	case "linux":
		if _, err := os.Stat("/run/systemd/system"); err == nil {
			return installSystemd(exe, cfgPath)
		}
		if _, err := os.Stat("/etc/init.d"); err == nil {
			return installInitd(exe, cfgPath)
		}
		return fmt.Errorf("未检测到 systemd 或 init.d")
	default:
		return fmt.Errorf("%s 暂不支持自动注册", runtime.GOOS)
	}
}

func installSystemd(exe, cfgPath string) error {
	unit := `[Unit]
Description=CC_DashBoard Agent
After=network.target

[Service]
ExecStart=` + exe + ` --config ` + cfgPath + `
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
`
	const path = "/etc/systemd/system/cc-agent.service"
	if err := os.WriteFile(path, []byte(unit), 0o644); err != nil {
		return err // 多半是权限不足
	}
	_ = exec.Command("systemctl", "daemon-reload").Run()
	_ = exec.Command("systemctl", "enable", "--now", "cc-agent").Run()
	return nil
}

func installInitd(exe, cfgPath string) error {
	// OpenWrt procd 模板
	script := `#!/bin/sh /etc/rc.common
USE_PROCD=1
START=99
PROG="` + exe + ` --config ` + cfgPath + `"

start_service() {
	procd_open_instance
	procd_set_param command $PROG
	procd_set_param respawn 3600 5 5
	procd_set_param stdout 1
	procd_set_param stderr 1
	procd_close_instance
}
`
	const path = "/etc/init.d/cc-agent"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		return err
	}
	_ = exec.Command(path, "enable").Run()
	_ = exec.Command(path, "restart").Run()
	return nil
}

func printManualService(cfgPath string) {
	exe, _ := os.Executable()
	fmt.Println("请手动注册服务：")
	fmt.Printf("  systemd: ExecStart=%s --config %s\n", exe, cfgPath)
	fmt.Printf("  Windows: sc.exe create cc-agent binPath= \"\\\"%s\\\" --config \\\"%s\\\"\" start= auto\n", exe, cfgPath)
}
