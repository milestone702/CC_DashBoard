//go:build windows

// service_windows.go Windows 服务模式：对接 SCM，解决 sc start 1053。
package main

import (
	"context"
	"log"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/svc"
)

// windowsService 由 initServiceMode 置位：当前进程是 SCM 启动的服务。
var windowsService bool

// initServiceMode 检测服务上下文并把日志改写到 exe 同目录文件——
// 服务模式下 stderr 会被 SCM 丢弃，不落盘就没有任何启动失败的线索。
// 必须在任何 log.Fatal 之前调用。
func initServiceMode() {
	isSvc, err := svc.IsWindowsService()
	if err != nil || !isSvc {
		return
	}
	windowsService = true
	exe, err := os.Executable()
	if err != nil {
		log.Println("⚠ 服务模式：获取 exe 路径失败，日志仅写 stderr")
		return
	}
	f, err := os.OpenFile(filepath.Join(filepath.Dir(exe), "cc-agent.log"),
		os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		log.Printf("⚠ 服务模式：打开日志文件失败: %v", err)
		return
	}
	log.SetOutput(f)
	log.Println("✓ 以 Windows 服务模式启动")
}

// startAgentMode 服务模式下向 SCM 注册处理器并接管启停生命周期；否则按控制台程序直接运行。
func startAgentMode(fn func(context.Context)) {
	if !windowsService {
		fn(context.Background())
		return
	}
	if err := svc.Run("cc-agent", &serviceHandler{fn: fn}); err != nil {
		log.Fatalf("服务模式运行失败: %v", err)
	}
}

// serviceHandler SCM 生命周期处理器：启动即上报 Running（1053 的关键），
// 收到 Stop/Shutdown 后取消 ctx 并等待采集循环退出。
type serviceHandler struct {
	fn func(context.Context)
}

func (h *serviceHandler) Execute(args []string, req <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.fn(ctx)
	}()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		cr := <-req
		switch cr.Cmd {
		case svc.Stop, svc.Shutdown:
			status <- svc.Status{State: svc.StopPending}
			cancel()
			<-done
			log.Println("✓ 收到停止请求，服务已退出")
			return false, 0
		default: // Interrogate / Checkpoint 等：回显当前状态
			status <- cr.CurrentStatus
		}
	}
}
