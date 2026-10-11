// cc-server --install 交互式安装：注册系统服务（Windows sc / Linux systemd / OpenWrt procd）。
package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

func runServerInstall() {
	fmt.Println("=== CC_DashBoard 服务端安装向导 ===")
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

	port := ask("监听端口", "8080")
	data := ask("数据目录", "./data")
	web := ask("前端目录（留空 = 使用内嵌前端）", "")

	args := fmt.Sprintf("-addr :%s -data %s", port, data)
	if web != "" {
		args += " -web " + web
	}
	args = strings.TrimSpace(args)

	exe, err := os.Executable()
	if err != nil {
		fmt.Printf("✗ 无法定位可执行文件: %v\n", err)
		return
	}

	if err := registerServerService(exe, args); err != nil {
		fmt.Printf("⚠ 自动注册失败: %v\n", err)
		fmt.Println("手动注册命令：")
		fmt.Printf("  systemd ExecStart=%s %s\n", exe, args)
		return
	}
	fmt.Println("✓ 服务端服务已注册并启动（开机自启）")
}

func registerServerService(exe, args string) error {
	switch runtime.GOOS {
	case "windows":
		bin := fmt.Sprintf(`"%s" %s`, exe, args)
		out, err := exec.Command("sc.exe", "create", "cc-server",
			"binPath=", bin, "start=", "auto",
			"DisplayName=", "CC_DashBoard Master").CombinedOutput()
		if err != nil {
			exec.Command("sc.exe", "delete", "cc-server").Run()
			out, err = exec.Command("sc.exe", "create", "cc-server",
				"binPath=", bin, "start=", "auto",
				"DisplayName=", "CC_DashBoard Master").CombinedOutput()
			if err != nil {
				return fmt.Errorf("%s", strings.TrimSpace(string(out)))
			}
		}
		exec.Command("sc.exe", "start", "cc-server").Run()
		return nil
	case "linux":
		if _, err := os.Stat("/run/systemd/system"); err == nil {
			return installServerSystemd(exe, args)
		}
		if _, err := os.Stat("/etc/init.d"); err == nil {
			return installServerInitd(exe, args)
		}
		return fmt.Errorf("未检测到 systemd 或 init.d")
	default:
		return fmt.Errorf("%s 暂不支持自动注册", runtime.GOOS)
	}
}

func installServerSystemd(exe, args string) error {
	unit := `[Unit]
Description=CC_DashBoard Master
After=network.target

[Service]
ExecStart=` + exe + ` ` + args +
		`
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
`
	const path = "/etc/systemd/system/cc-server.service"
	if err := os.WriteFile(path, []byte(unit), 0o644); err != nil {
		return err
	}
	_ = exec.Command("systemctl", "daemon-reload").Run()
	_ = exec.Command("systemctl", "enable", "--now", "cc-server").Run()
	return nil
}

func installServerInitd(exe, args string) error {
	script := `#!/bin/sh /etc/rc.common
START=99
PROG="` + exe + " " + args + `"

start_service() {
	procd_open_instance
	procd_set_param command $PROG
	procd_set_param respawn 3600 5 5
	procd_close_instance
}
`
	const path = "/etc/init.d/cc-server"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		return err
	}
	_ = exec.Command(path, "enable").Run()
	_ = exec.Command(path, "restart").Run()
	return nil
}
