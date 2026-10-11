// OTA 升级与身份元数据上报：公网 IP 自查、开关机记录采集、升级下载替换与回滚。
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/shirou/gopsutil/v3/host"
)

// targetArch 池匹配用的目标架构：ARM 用 uname -m 细分（armv7l→armv7），其余即 GOARCH。
func targetArch() string {
	if runtime.GOARCH == "arm" && runtime.GOOS == "linux" {
		cmd := exec.Command("uname", "-m")
		if out, err := cmd.Output(); err == nil {
			m := strings.TrimSpace(string(out))
			if strings.HasPrefix(m, "armv") {
				return strings.TrimSuffix(m, "l")
			}
		}
	}
	return runtime.GOARCH
}

// ---------- 公网 IP 自查（上报兜底用；失败静默，缓存 30 分钟） ----------

var (
	pubIPMu sync.Mutex
	pubIP   string
)

func getPublicIP() string {
	pubIPMu.Lock()
	defer pubIPMu.Unlock()
	return pubIP
}

func setPublicIP(v string) {
	pubIPMu.Lock()
	pubIP = v
	pubIPMu.Unlock()
}

// startPublicIPLoop 启动后 3 秒首查，之后每 30 分钟刷新。
func startPublicIPLoop() {
	time.Sleep(3 * time.Second)
	fetchPublicIP()
	for range time.Tick(30 * time.Minute) {
		fetchPublicIP()
	}
}

func fetchPublicIP() {
	client := &http.Client{Timeout: 5 * time.Second}
	for _, u := range []string{"https://api.ipify.org", "https://checkip.amazonaws.com"} {
		resp, err := client.Get(u)
		if err != nil {
			continue
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64))
		resp.Body.Close()
		if ip := parsePublicIP(string(b)); ip != "" {
			setPublicIP(ip)
			return
		}
	}
}

func parsePublicIP(s string) string {
	ip := net.ParseIP(strings.TrimSpace(s))
	if ip == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return ""
	}
	return ip.String()
}

// ---------- 开关机记录采集 ----------

type bootEvent struct {
	TS   int64  `json:"ts"`
	Type string `json:"type"` // on | off
}

var (
	bootMu      sync.Mutex
	bootCached  []bootEvent
	bootLastUpd time.Time
)

// bootEventsSnapshot 返回缓存的开关机记录（超 1 分钟先刷新再取）。
func bootEventsSnapshot() []bootEvent {
	bootMu.Lock()
	if time.Since(bootLastUpd) > time.Minute {
		bootMu.Unlock()
		refreshBootEvents()
		bootMu.Lock()
	}
	out := make([]bootEvent, len(bootCached))
	copy(out, bootCached)
	bootMu.Unlock()
	return out
}

func refreshBootEvents() {
	evs := collectBootEvents()
	bootMu.Lock()
	bootCached = evs
	bootLastUpd = time.Now()
	bootMu.Unlock()
}

func collectBootEvents() []bootEvent {
	if runtime.GOOS == "windows" {
		if evs := collectBootEventsPowerShell(); len(evs) > 0 {
			return evs
		}
	} else if evs := collectBootEventsLast(); len(evs) > 0 {
		return evs
	}
	// 回退：仅当前开机时间
	if up, err := host.Uptime(); err == nil {
		return []bootEvent{{TS: time.Now().Unix() - int64(up), Type: "on"}}
	}
	return nil
}

// collectBootEventsLast 解析 `last -x`（wtmp）：reboot=on、shutdown=off。
func collectBootEventsLast() []bootEvent {
	out, err := runLast("-x", "-F")
	if err != nil {
		if out, err = runLast("-x"); err != nil {
			return nil
		}
	}
	var evs []bootEvent
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		var typ string
		switch {
		case f[0] == "reboot" && len(f) >= 4 && f[1] == "system" && f[2] == "boot":
			typ = "on"
		case f[0] == "shutdown" && len(f) >= 4 && f[1] == "system" && f[2] == "down":
			typ = "off"
		default:
			continue
		}
		if ts := parseLastTime(f[3:]); ts > 0 {
			evs = append(evs, bootEvent{TS: ts, Type: typ})
		}
	}
	return evs
}

func runLast(args ...string) (string, error) {
	cmd := exec.Command("last", args...)
	cmd.Env = append(os.Environ(), "LANG=C", "LC_ALL=C")
	out, err := cmd.Output()
	return string(out), err
}

// parseLastTime 解析 last 时间字段：
// `-F` 全时间 "Mon Oct 10 16:35:00 2026"；busybox 默认 "Mon Oct 10 16:35 [2025]"。
func parseLastTime(f []string) int64 {
	if len(f) >= 5 {
		if ts, err := time.Parse("Mon Jan _2 15:04:05 2006", strings.Join(f[:5], " ")); err == nil {
			return ts.Unix()
		}
		if ts, err := time.Parse("Mon Jan _2 15:04 2006", strings.Join(f[:5], " ")); err == nil {
			return ts.Unix()
		}
	}
	if len(f) >= 4 {
		if ts, err := time.Parse("Mon Jan _2 15:04", strings.Join(f[:4], " ")); err == nil {
			// 无年份：按当前年解释（wtmp 通常只覆盖近期）
			now := time.Now()
			ts = time.Date(now.Year(), ts.Month(), ts.Day(), ts.Hour(), ts.Minute(), 0, 0, time.Local)
			return ts.Unix()
		}
	}
	return 0
}

// collectBootEventsPowerShell 读 Windows 系统事件日志 6005(开机)/6006(关机)。
func collectBootEventsPowerShell() []bootEvent {
	ps := `Get-WinEvent -FilterHashtable @{LogName='System';Id=6005,6006} -MaxEvents 20 | ` +
		`ForEach-Object { "$($_.Id) $($_.TimeCreated.ToUniversalTime().ToString('o'))" }`
	out, err := exec.Command("powershell", "-NoProfile", "-Command", ps).Output()
	if err != nil {
		return nil
	}
	var evs []bootEvent
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(strings.TrimSpace(line))
		if len(f) < 2 {
			continue
		}
		var typ string
		switch f[0] {
		case "6005":
			typ = "on"
		case "6006":
			typ = "off"
		default:
			continue
		}
		if ts, err := time.Parse(time.RFC3339, f[1]); err == nil {
			evs = append(evs, bootEvent{TS: ts.Unix(), Type: typ})
		}
	}
	return evs
}

// ---------- OTA 升级 ----------

// upgradeInfo 服务端 /report 响应中的升级指令。
type upgradeInfo struct {
	Version string `json:"version"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size"`
}

// pendingMark 升级挂起标记（exe 同目录 .pending），用于升级确认与失败回滚。
type pendingMark struct {
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
	TS      int64  `json:"ts"`
}

var (
	otaMu      sync.Mutex
	otaRunning bool
	otaLastVer string
	otaLastTry time.Time
	lastOK     atomic.Int64 // 最近一次上报成功的 unix 秒
)

func markReportOK() { lastOK.Store(time.Now().Unix()) }

// maybeStartUpgrade 收到升级指令后限流启动后台升级。
func maybeStartUpgrade(master, token string, up *upgradeInfo) {
	if up == nil || up.Version == "" || up.Version == version {
		return
	}
	if detectOSType() == "openwrt" { // 防御：服务端不下发，这里再挡一层
		return
	}
	otaMu.Lock()
	if otaRunning || (up.Version == otaLastVer && time.Since(otaLastTry) < 15*time.Minute) {
		otaMu.Unlock()
		return
	}
	otaRunning, otaLastVer, otaLastTry = true, up.Version, time.Now()
	otaMu.Unlock()
	go func() {
		defer func() {
			otaMu.Lock()
			otaRunning = false
			otaMu.Unlock()
		}()
		if err := applyUpgrade(master, token, up); err != nil {
			log.Printf("OTA 升级到 %s 失败: %v", up.Version, err)
		}
	}()
}

func applyUpgrade(master, token string, up *upgradeInfo) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("定位自身失败: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	tmp := exe + ".new"
	defer os.Remove(tmp) // swap 成功后该文件已被 rename，此处移除是空操作

	if err := downloadVerify(strings.TrimRight(master, "/")+up.URL, token, tmp, up.SHA256, up.Size); err != nil {
		return err
	}
	// 先落升级标记（含旧版备份路径），再换文件——中途崩溃也能回滚
	mark := pendingMark{Version: up.Version, SHA256: up.SHA256, TS: time.Now().Unix()}
	if err := writePending(exe, &mark); err != nil {
		return err
	}
	if err := swapBinary(exe, tmp, exe+".old"); err != nil {
		_ = os.Remove(exe + ".pending")
		return err
	}
	log.Printf("✓ 已下载并替换为 %s，正在重启…", up.Version)
	restartAfterSwap(exe)
	return nil
}

func downloadVerify(url, token, dst, wantSHA string, wantSize int64) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-CC-Node-Token", token)
	resp, err := (&http.Client{Timeout: 10 * time.Minute}).Do(req)
	if err != nil {
		return fmt.Errorf("下载失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("下载失败 HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), resp.Body)
	closeErr := f.Close()
	if err != nil {
		return fmt.Errorf("写入失败: %w", err)
	}
	if closeErr != nil {
		return closeErr
	}
	if wantSize > 0 && n != wantSize {
		return fmt.Errorf("大小不匹配: got %d want %d", n, wantSize)
	}
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, wantSHA) {
		return fmt.Errorf("sha256 校验失败: got %s want %s", got, wantSHA)
	}
	return nil
}

func pendingPath(exe string) string { return exe + ".pending" }

func writePending(exe string, m *pendingMark) error {
	b, _ := json.Marshal(m)
	return os.WriteFile(pendingPath(exe), b, 0o600)
}

func readPending(exe string) *pendingMark {
	b, err := os.ReadFile(pendingPath(exe))
	if err != nil {
		return nil
	}
	var m pendingMark
	if json.Unmarshal(b, &m) != nil || m.TS == 0 {
		return nil
	}
	return &m
}

// startOTAFinalize 升级挂起标记的确认与回滚观察器（启动即运行一次生命周期）。
// 连续观察到两次“新的上报成功”→ 确认升级完成，清理备份；10 分钟仍未稳定 → 还原旧版并重启。
func startOTAFinalize() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	mark := readPending(exe)
	if mark == nil {
		return
	}
	log.Printf("检测到 OTA 升级挂起（%s），等待稳定确认…", mark.Version)
	var prev int64
	stable := 0
	deadline := time.Now().Add(10 * time.Minute)
	for time.Now().Before(deadline) {
		cur := lastOK.Load()
		if cur > mark.TS && cur > prev {
			stable++
			prev = cur
			if stable >= 2 {
				_ = os.Remove(pendingPath(exe))
				_ = os.Remove(exe + ".old")
				log.Printf("✓ OTA 升级确认完成: %s", mark.Version)
				return
			}
		}
		time.Sleep(15 * time.Second)
	}
	log.Printf("OTA 升级后 10 分钟未稳定上报，回滚到旧版本…")
	if err := restoreBinary(exe, exe+".old"); err != nil {
		log.Printf("回滚失败: %v", err)
		_ = os.Remove(pendingPath(exe))
		return
	}
	_ = os.Remove(pendingPath(exe))
	restartAfterSwap(exe)
}
