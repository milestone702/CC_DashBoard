//go:build windows

// OTA 平台相关：Windows 的自替换（运行中 exe 不可覆盖，先 rename 腾位）与服务/前台重启。
package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

const (
	detachedProcess = 0x00000008 // DETACHED_PROCESS
	createNoWindow  = 0x08000000 // CREATE_NO_WINDOW
)

// swapBinary 运行中的 exe 允许 rename 但不允许覆盖：先把自身改名为备份，再把新文件就位。
func swapBinary(exe, newPath, backupPath string) error {
	if err := os.Rename(exe, backupPath); err != nil {
		return fmt.Errorf("备份旧版本失败: %w", err)
	}
	if err := os.Rename(newPath, exe); err != nil {
		// 尽力还原备份，保证可执行文件仍在原位
		_ = os.Rename(backupPath, exe)
		return fmt.Errorf("替换失败: %w", err)
	}
	return nil
}

// restoreBinary 回滚：当前（新版）先让位，再把旧版 rename 回原位。
func restoreBinary(exe, backupPath string) error {
	if _, err := os.Stat(backupPath); err != nil {
		return fmt.Errorf("旧版本备份不存在: %w", err)
	}
	if err := os.Rename(exe, exe+".bad"); err != nil {
		return err
	}
	return os.Rename(backupPath, exe)
}

// restartAfterSwap 服务模式：拉起延迟 stop/start 命令后退出自身；
// 前台模式：拉起新进程（脱离控制台）后退出。
func restartAfterSwap(exe string) {
	if isOurWindowsService() {
		cmd := exec.Command("cmd", "/C",
			`ping -n 3 127.0.0.1 >NUL & net stop cc-agent & net start cc-agent`)
		cmd.SysProcAttr = &syscall.SysProcAttr{
			HideWindow:    true,
			CreationFlags: createNoWindow,
		}
		if err := cmd.Start(); err != nil {
			log.Printf("拉起服务重启命令失败（请手动重启 cc-agent 服务）: %v", err)
		}
		log.Println("服务模式：重启中（延迟拉起新版本）…")
		os.Exit(0)
	}
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: detachedProcess,
	}
	if err := cmd.Start(); err != nil {
		log.Printf("拉起新进程失败（请手动重启）: %v", err)
		os.Exit(1)
	}
	log.Println("已以后台方式重启新版本，当前进程退出")
	os.Exit(0)
}

// isOurWindowsService 当前进程是否为 cc-agent Windows 服务（sc queryex 的 PID 与自身一致）。
func isOurWindowsService() bool {
	out, err := exec.Command("sc.exe", "queryex", "cc-agent").Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "PID") {
			continue
		}
		if parts := strings.SplitN(line, ":", 2); len(parts) == 2 {
			if pid, err := strconv.Atoi(strings.TrimSpace(parts[1])); err == nil && pid == os.Getpid() {
				return true
			}
		}
	}
	return false
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
