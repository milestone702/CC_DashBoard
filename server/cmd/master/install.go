// cc-dashboard --install 交互式安装：注册系统服务（Windows sc / Linux systemd / OpenWrt procd）。
package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// 服务名统一为 cc-dashboard：与 deb/rpm 包名、/usr/bin 下的二进制名、systemd unit 一致。
// 旧版 --install 用的是 cc-server，注册前会先清理，避免升级后两个服务抢同一个端口。
const (
	serviceName       = "cc-dashboard"
	legacyServiceName = "cc-server"
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
		cleanupLegacyWindows()
		out, err := exec.Command("sc.exe", "create", serviceName,
			"binPath=", bin, "start=", "auto",
			"DisplayName=", "CC_DashBoard Master").CombinedOutput()
		if err != nil {
			exec.Command("sc.exe", "delete", serviceName).Run()
			out, err = exec.Command("sc.exe", "create", serviceName,
				"binPath=", bin, "start=", "auto",
				"DisplayName=", "CC_DashBoard Master").CombinedOutput()
			if err != nil {
				return fmt.Errorf("%s", strings.TrimSpace(string(out)))
			}
		}
		exec.Command("sc.exe", "start", serviceName).Run()
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

// cleanupLegacyWindows 删除旧版注册的 cc-server 服务（升级场景），
// 否则新旧两个服务会同时监听同一端口，新服务起不来。
func cleanupLegacyWindows() {
	if _, err := exec.Command("sc.exe", "query", legacyServiceName).CombinedOutput(); err != nil {
		return // 不存在
	}
	exec.Command("sc.exe", "stop", legacyServiceName).Run()
	time.Sleep(500 * time.Millisecond) // 等 SCM 完成停止，否则 delete 会被拒
	exec.Command("sc.exe", "delete", legacyServiceName).Run()
	fmt.Printf("· 已清理旧服务 %s（升级自旧版本）\n", legacyServiceName)
}

// cleanupLegacyPath 停用并删除旧版 systemd 单元 / init.d 脚本。
func cleanupLegacyPath(path, name string) {
	if _, err := os.Stat(path); err != nil {
		return
	}
	_ = exec.Command("systemctl", "disable", "--now", name).Run()
	_ = exec.Command(path, "disable").Run()
	_ = exec.Command(path, "stop").Run()
	_ = os.Remove(path)
	_ = exec.Command("systemctl", "daemon-reload").Run()
	fmt.Printf("· 已清理旧服务 %s（升级自旧版本）\n", path)
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
	cleanupLegacyPath("/etc/systemd/system/"+legacyServiceName+".service", legacyServiceName)
	path := "/etc/systemd/system/" + serviceName + ".service"
	if err := os.WriteFile(path, []byte(unit), 0o644); err != nil {
		return err
	}
	_ = exec.Command("systemctl", "daemon-reload").Run()
	_ = exec.Command("systemctl", "enable", "--now", serviceName).Run()
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
	cleanupLegacyPath("/etc/init.d/"+legacyServiceName, legacyServiceName)
	path := "/etc/init.d/" + serviceName
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		return err
	}
	_ = exec.Command(path, "enable").Run()
	_ = exec.Command(path, "restart").Run()
	return nil
}
