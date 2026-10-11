//go:build !windows

// service_other.go 非 Windows 平台无服务协议，提供与 Windows 侧相同的入口桩；
// Linux 服务由 systemd/init.d 直接拉起进程（见 install.go）。
package main

import "context"

func initServiceMode() {}

func startAgentMode(fn func(context.Context)) {
	fn(context.Background())
}
