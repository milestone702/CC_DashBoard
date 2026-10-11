//go:build !windows

// OTA 平台相关：Linux（systemd / init.d / 前台）的文件替换与重启。
package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"strings"
)

// swapBinary 将 newPath 落到 exe 位置：先备份当前可执行文件，再原子 rename 覆盖
//（Linux 允许 rename 覆盖运行中的可执行文件，避免 ETXTBSY）。
func swapBinary(exe, newPath, backupPath string) error {
	if err := copyFile(exe, backupPath, 0o755); err != nil {
		return fmt.Errorf("备份旧版本失败: %w", err)
	}
	if err := os.Chmod(newPath, 0o755); err != nil {
		return fmt.Errorf("设置可执行权限失败: %w", err)
	}
	if err := os.Rename(newPath, exe); err != nil {
		return fmt.Errorf("替换失败: %w", err)
	}
	return nil
}

// restoreBinary 回滚：把备份 rename 回 exe（覆盖运行中的自身无碍）。
func restoreBinary(exe, backupPath string) error {
	if _, err := os.Stat(backupPath); err != nil {
		return fmt.Errorf("旧版本备份不存在: %w", err)
	}
	return os.Rename(backupPath, exe)
}

// restartAfterSwap 重启：systemd 托管（cgroup 含服务名）时直接退出，
// Restart=always 会在 RestartSec 后拉起新版本；前台运行只提示手动重启。
func restartAfterSwap(exe string) {
	if b, err := os.ReadFile("/proc/self/cgroup"); err == nil &&
		strings.Contains(string(b), "cc-agent") {
		log.Println("重启中（systemd 托管，退出后自动拉起新版本）…")
		os.Exit(0)
	}
	log.Println("已替换为新版本；当前为前台运行，不自动重启，请手动重启进程后生效")
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
