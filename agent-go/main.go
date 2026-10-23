// CC_DashBoard Go Agent：周期采集本机指标、实时明细与硬件清单并上报 Master。
// 运行模式：
//
//	cc-agent -master http://s:8080 -token xxx     反向连接（默认，Agent 主动上报）
//	cc-agent --install                            交互式注册服务（开发中）
//
// 交叉编译：
//
//	GOOS=linux GOARCH=amd64|arm64|armv7|mipsle CGO_ENABLED=0 go build -o cc-agent .
//	GOOS=windows GOARCH=amd64 go build -o cc-agent.exe .
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	stdnet "net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/load"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/net"
	"github.com/shirou/gopsutil/v3/process"
)

// version 构建时注入：go build -ldflags "-X main.version=$(git describe --tags)"
var version = "dev"

func main() {
	master := flag.String("master", "http://127.0.0.1:8080", "Master 地址（反向模式）")
	token := flag.String("token", "", "节点 Token（留空 + --config 时首次启动自动注册生成）")
	interval := flag.Duration("interval", 5*time.Second, "上报/采样周期")
	mode := flag.String("mode", "push", "连接模式：push=反向上报（默认）| listen=正向监听（Server 拉取）")
	listen := flag.String("listen", ":9100", "正向模式监听地址（-mode=listen 时生效）")
	install := flag.Bool("install", false, "交互式安装：写入配置文件并注册系统服务")
	configPath := flag.String("config", "", "配置文件路径（服务模式加载）")
	showVer := flag.Bool("version", false, "打印版本号后退出")
	flag.Parse()

	if *showVer {
		fmt.Println(version)
		return
	}

	if *install {
		runInstall()
		return
	}
	initServiceMode() // 服务模式检测 + 日志重定向，须在任何 log.Fatal 之前
	// --config：为未显式指定的 flag 提供默认值（flag 优先级更高）
	if *configPath != "" {
		cfg, err := loadConfig(*configPath)
		if err != nil {
			log.Fatalf("加载配置失败 %s: %v", *configPath, err)
		}
		set := map[string]bool{}
		flag.Visit(func(f *flag.Flag) { set[f.Name] = true })
		if !set["master"] && cfg.Master != "" {
			*master = cfg.Master
		}
		if !set["token"] && cfg.Token != "" {
			*token = cfg.Token
		}
		if !set["mode"] && cfg.Mode != "" {
			*mode = cfg.Mode
		}
		if !set["listen"] && cfg.Listen != "" {
			*listen = cfg.Listen
		}
		if !set["interval"] && cfg.IntervalSec > 0 {
			*interval = time.Duration(cfg.IntervalSec) * time.Second
		}
	}

	// 模板占位符视同未填：触发首次启动自动注册
	if *token == tokenPlaceholder {
		*token = ""
	}
	if *token == "" && *configPath == "" {
		log.Fatal("缺少 -token：指定 --config 后首次启动会自动注册，或从管理面板创建节点获取")
	}

	// ensureToken：无 token 时向 Master 自注册（服务端生成，后台批准准入），并写回配置。
	ensureToken := func() bool {
		if *token != "" {
			return true
		}
		tok, approved, err := registerNode(*master)
		if err != nil {
			log.Printf("自动注册失败: %v", err)
			return false
		}
		*token = tok
		if err := saveToken(*configPath, tok); err != nil {
			log.Printf("⚠ token 已获取但写回 %s 失败: %v（重启会重新注册）", *configPath, err)
		} else {
			log.Printf("✓ 首次启动自动注册成功，token 已写入 %s", *configPath)
		}
		if !approved {
			log.Println("节点待管理员批准：在管理后台点「批准」后上报才会被接受")
		}
		return true
	}

	// startAgentMode：Windows 服务模式接管 SCM 启停，其他场景同步运行直到进程退出。
	startAgentMode(func(ctx context.Context) {
		s := &sampler{}
		_, _, _, _, _ = s.sample() // 首次采集建立差分基线
		refreshBootEvents()        // 开关机记录首采（last / 事件日志）
		go startPublicIPLoop()     // 公网 IP 自查（上报兜底）
		go startOTAFinalize()      // 升级挂起标记：确认或回滚

		// 采样缓存：两种模式共用；push 直接上报，listen 由 /metrics 提供
		var mu sync.Mutex
		var cached []byte
		// 上报心跳：首次成功 + 之后每分钟打一条，否则成功时静默无法排查
		var lastReportOK time.Time

		runCycle := func() {
			core, detail, live, hw, err := s.sample()
			if err != nil {
				log.Printf("采集失败: %v", err)
				return
			}
			payload, err := buildPayload(core, detail, live, hw)
			if err != nil {
				log.Printf("序列化失败: %v", err)
				return
			}
			mu.Lock()
			cached = payload
			mu.Unlock()
			if *mode == "push" {
				if !ensureToken() {
					return
				}
				up, err := postPayload(*master, *token, payload)
				if err != nil {
					log.Printf("上报失败: %v", err)
					return
				}
				if lastReportOK.IsZero() {
					log.Printf("✓ 首次上报成功 → %s", *master)
				} else if time.Since(lastReportOK) >= time.Minute {
					log.Printf("✓ 上报正常（已持续 %d 分钟）", int(time.Since(lastReportOK).Minutes()))
				}
				lastReportOK = time.Now()
				markReportOK()
				maybeStartUpgrade(*master, *token, up)
			}
		}

		runCycle()

		if *mode == "listen" {
			ensureToken() // 正向模式同样先自动注册拿 token
			mux := http.NewServeMux()
			mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
				if *token == "" || r.Header.Get("X-CC-Node-Token") != *token {
					http.Error(w, "unauthorized", http.StatusUnauthorized)
					return
				}
				mu.Lock()
				body := cached
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(body)
			})
			go func() {
				log.Printf("正向模式：监听 %s（GET /metrics）", *listen)
				if err := http.ListenAndServe(*listen, mux); err != nil {
					log.Printf("监听失败: %v", err)
				}
			}()
		}

		ticker := time.NewTicker(*interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				log.Println("收到停止信号，退出采集循环")
				return
			case <-ticker.C:
				runCycle()
			}
		}
	})
}

// sampler 保留上次采样状态，用于 CPU/网卡/磁盘 I/O 差分；低频数据按周期降采。
type sampler struct {
	prevTS       time.Time
	prevNicTS    time.Time
	prevDiskTS   time.Time
	prevCPUTimes []cpu.TimesStat
	prevPerCPU   []cpu.TimesStat
	prevNet      map[string]net.IOCountersStat
	prevNetTotal net.IOCountersStat
	hasNet       bool
	prevDiskIO   disk.IOCountersStat
	hasDiskIO    bool
	cycles       int
	hardware     map[string]any   // 硬件清单缓存（每 20 周期刷新）
	lastProcs    []map[string]any // 低频字段缓存：每周期都随 live 上报
	lastTemps    []map[string]any
	procCPU      map[int32]procStat // 进程 CPU 跨周期差分基线
}

type procStat struct {
	total float64
	ts    time.Time
}

func rootPath() string {
	if runtime.GOOS == "windows" {
		return `C:\`
	}
	return "/"
}

// sample 一次采样：core(定宽指标) / detail(系统详情) / live(实时明细) / hardware(硬件清单)。
func (s *sampler) sample() (core, detail, live, hardware map[string]any, err error) {
	now := time.Now()
	s.cycles++

	// ---- 定宽核心指标（进历史表） ----
	cpuPct, _, err := s.cpuPercent()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	percpu, err := s.perCPU()
	if err != nil {
		percpu = nil
	}
	vm, err := mem.VirtualMemory()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	du, err := disk.Usage(rootPath())
	if err != nil {
		return nil, nil, nil, nil, err
	}
	rx, tx, err := s.netRates(now)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	var avg *load.AvgStat
	if a, e := load.Avg(); e == nil {
		avg = a
	}
	uptime, _ := host.Uptime()
	dio, dioRate, dioErr := s.diskIORates(now) // 单次调用：core 与 live 共用（差分基线只推进一次）

	core = map[string]any{
		"cpu": round2(cpuPct), "mem_used": int64(vm.Used), "mem_total": int64(vm.Total),
		"disk_pct": round1(du.UsedPercent), "net_rx": rx, "net_tx": tx,
		"load1": avgVal(avg, 0), "uptime": int64(uptime),
		// 磁盘 I/O 速率与每核使用率进历史表（曲线图数据源）
		"per_cpu": percpu,
	}
	if dioErr == nil {
		core["disk_read"] = dioRate[0]
		core["disk_write"] = dioRate[1]
	} else {
		core["disk_read"] = int64(0)
		core["disk_write"] = int64(0)
	}

	// ---- 实时明细（每周期；进程 Top5 每 3 周期，温度每 6 周期） ----
	live = map[string]any{
		"ts":        now.Unix(),
		"cpu_total": round2(cpuPct),
		"per_cpu":   percpu,
		"load1":     avgVal(avg, 0), "load5": avgVal(avg, 1), "load15": avgVal(avg, 2),
		"mem": map[string]any{
			"total": int64(vm.Total), "used": int64(vm.Used), "available": int64(vm.Available),
			"free": int64(vm.Free), "buffers": int64(vm.Buffers), "cached": int64(vm.Cached),
			"swap_total": int64(vm.SwapTotal), "swap_used": int64(vm.SwapTotal - vm.SwapFree),
			"percent": round1(vm.UsedPercent),
		},
	}
	if dioErr == nil {
		live["disk_io"] = map[string]any{
			"read_bps": dioRate[0], "write_bps": dioRate[1],
			"read_count": int64(dio.ReadCount), "write_count": int64(dio.WriteCount),
		}
	}
	live["nics"] = s.nicRates(now)

	if s.cycles%6 == 1 {
		s.lastTemps = readTemps()
	}
	if s.cycles%3 == 1 {
		s.lastProcs = s.topProcesses(5)
	}
	// 低频字段每周期都带上（缓存值），避免后续上报覆盖丢失
	if s.lastTemps == nil {
		s.lastTemps = []map[string]any{}
	}
	if s.lastProcs == nil {
		s.lastProcs = []map[string]any{}
	}
	live["temps"] = s.lastTemps
	live["procs"] = s.lastProcs

	// ---- 系统详情（原 detail）与硬件清单 ----
	detail = buildDetail()
	if s.hardware == nil || s.cycles%20 == 1 {
		s.hardware = buildHardware()
	}
	hardware = s.hardware
	return core, detail, live, hardware, nil
}

// cpuPercent 基于两次 cpu.Times 差分（不阻塞）。
func (s *sampler) cpuPercent() (float64, []cpu.TimesStat, error) {
	cur, err := cpu.Times(false)
	if err != nil {
		return 0, nil, err
	}
	if len(s.prevCPUTimes) == 0 || len(cur) != len(s.prevCPUTimes) {
		s.prevCPUTimes = cur
		return timesBusyPct(cur[0]), cur, nil
	}
	prev, c0 := s.prevCPUTimes[0], cur[0]
	s.prevCPUTimes = cur
	dTotal := timesTotal(c0) - timesTotal(prev)
	dBusy := timesBusy(c0) - timesBusy(prev)
	if dTotal <= 0 {
		return 0, cur, nil
	}
	return clampPct(dBusy / dTotal * 100), cur, nil
}

// perCPU 每核使用率差分。
func (s *sampler) perCPU() ([]float64, error) {
	cur, err := cpu.Times(true)
	if err != nil {
		return nil, err
	}
	out := make([]float64, len(cur))
	if len(s.prevPerCPU) != len(cur) {
		for i := range cur {
			out[i] = round2(timesBusyPct(cur[i]))
		}
		s.prevPerCPU = cur
		return out, nil
	}
	for i := range cur {
		dTotal := timesTotal(cur[i]) - timesTotal(s.prevPerCPU[i])
		dBusy := timesBusy(cur[i]) - timesBusy(s.prevPerCPU[i])
		if dTotal > 0 {
			out[i] = round2(clampPct(dBusy / dTotal * 100))
		}
	}
	s.prevPerCPU = cur
	return out, nil
}

// netRates 全接口合计上下行速率（Bytes/s）。
func (s *sampler) netRates(now time.Time) (rx, tx int64, err error) {
	counters, err := net.IOCounters(false)
	if err != nil || len(counters) == 0 {
		return 0, 0, err
	}
	cur := counters[0]
	if !s.hasNet {
		s.prevNetTotal, s.prevTS, s.hasNet = cur, now, true
		return 0, 0, nil
	}
	elapsed := now.Sub(s.prevTS).Seconds()
	dRx := int64(cur.BytesRecv) - int64(s.prevNetTotal.BytesRecv)
	dTx := int64(cur.BytesSent) - int64(s.prevNetTotal.BytesSent)
	s.prevNetTotal, s.prevTS = cur, now
	if elapsed <= 0 || dRx < 0 || dTx < 0 {
		return 0, 0, nil
	}
	return int64(float64(dRx) / elapsed), int64(float64(dTx) / elapsed), nil
}

// nicRates 分网卡实时速率。
func (s *sampler) nicRates(now time.Time) []map[string]any {
	per, err := net.IOCounters(true)
	if err != nil {
		return nil
	}
	// MAC/IP 映射
	meta := map[string]map[string]string{}
	if list, err := net.Interfaces(); err == nil {
		for _, iface := range list {
			ipv4 := ""
			for _, a := range iface.Addrs {
				if v := ipv4Of(a.Addr); v != "" {
					ipv4 = v
					break
				}
			}
			meta[iface.Name] = map[string]string{"mac": iface.HardwareAddr, "ipv4": ipv4}
		}
	}
	out := []map[string]any{}
	if s.prevNet == nil {
		s.prevNet = map[string]net.IOCountersStat{}
	}
	elapsed := 0.0
	if !s.prevNicTS.IsZero() {
		elapsed = now.Sub(s.prevNicTS).Seconds()
	}
	for _, c := range per {
		rx, tx := int64(0), int64(0)
		if p, ok := s.prevNet[c.Name]; ok && elapsed > 0 {
			if d := int64(c.BytesRecv) - int64(p.BytesRecv); d >= 0 {
				rx = int64(float64(d) / elapsed)
			}
			if d := int64(c.BytesSent) - int64(p.BytesSent); d >= 0 {
				tx = int64(float64(d) / elapsed)
			}
		}
		s.prevNet[c.Name] = c
		m := meta[c.Name]
		out = append(out, map[string]any{
			"name": c.Name, "rx_bps": rx, "tx_bps": tx,
			"mac": m["mac"], "ipv4": m["ipv4"],
		})
	}
	s.prevNicTS = now
	return out
}

// diskIORates 磁盘 I/O 速率（全盘合计）。
func (s *sampler) diskIORates(now time.Time) (disk.IOCountersStat, [2]int64, error) {
	m, err := disk.IOCounters()
	if err != nil {
		return disk.IOCountersStat{}, [2]int64{}, err
	}
	// 返回全盘 map，聚合求和得到合计
	var cur disk.IOCountersStat
	for _, v := range m {
		cur.ReadBytes += v.ReadBytes
		cur.WriteBytes += v.WriteBytes
		cur.ReadCount += v.ReadCount
		cur.WriteCount += v.WriteCount
	}
	var rate [2]int64
	if !s.hasDiskIO {
		s.prevDiskIO, s.hasDiskIO, s.prevDiskTS = cur, true, now
		return cur, rate, nil
	}
	elapsed := now.Sub(s.prevDiskTS).Seconds()
	if elapsed > 0 {
		if d := int64(cur.ReadBytes) - int64(s.prevDiskIO.ReadBytes); d >= 0 {
			rate[0] = int64(float64(d) / elapsed)
		}
		if d := int64(cur.WriteBytes) - int64(s.prevDiskIO.WriteBytes); d >= 0 {
			rate[1] = int64(float64(d) / elapsed)
		}
	}
	s.prevDiskIO, s.prevDiskTS = cur, now
	return cur, rate, nil
}

// readTemps 芯片温度：Linux 读 thermal_zone（无传感器或非 Linux 返回空数组）。
func readTemps() []map[string]any {
	out := []map[string]any{}
	if runtime.GOOS != "linux" {
		return out
	}
	paths, err := filepath.Glob("/sys/class/thermal/thermal_zone*/temp")
	if err != nil {
		return out
	}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		milli, err := strconv.Atoi(strings.TrimSpace(string(b)))
		if err != nil {
			continue
		}
		label := "temp"
		if tb, err := os.ReadFile(filepath.Dir(p) + "/type"); err == nil {
			label = strings.TrimSpace(string(tb))
		}
		out = append(out, map[string]any{
			"label": label, "value": round1(float64(milli) / 1000),
		})
	}
	return out
}

// topProcesses CPU 占用 TopN：进程 CPU 时间跨周期差分（非阻塞、Windows 可靠）。
func (s *sampler) topProcesses(n int) []map[string]any {
	pids, err := process.Pids()
	if err != nil || len(pids) == 0 {
		return []map[string]any{}
	}
	now := time.Now()
	if s.procCPU == nil {
		s.procCPU = map[int32]procStat{}
	}
	seen := map[int32]bool{}
	type row struct {
		pid      int32
		name     string
		cpu, mem float64
	}
	rows := make([]row, 0, len(pids))
	cores := float64(runtime.NumCPU())
	for _, pid := range pids {
		p, err := process.NewProcess(pid)
		if err != nil {
			continue
		}
		seen[pid] = true
		cpuP := 0.0
		if t, err := p.Times(); err == nil {
			cur := t.Total()
			if prev, ok := s.procCPU[pid]; ok {
				if el := now.Sub(prev.ts).Seconds(); el >= 0.4 && cur >= prev.total {
					cpuP = (cur - prev.total) / el / cores * 100
					if cpuP > 100 {
						cpuP = 100
					}
				}
			}
			s.procCPU[pid] = procStat{total: cur, ts: now}
		}
		name, _ := p.Name()
		memP, _ := p.MemoryPercent()
		rows = append(rows, row{pid: pid, name: name, cpu: cpuP, mem: float64(memP)})
	}
	for pid := range s.procCPU {
		if !seen[pid] {
			delete(s.procCPU, pid)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].cpu > rows[j].cpu })
	if len(rows) > n {
		rows = rows[:n]
	}
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		out = append(out, map[string]any{
			"pid": r.pid, "name": r.name,
			"cpu": round1(r.cpu), "mem": round1(r.mem),
		})
	}
	return out
}

// buildDetail 系统详情（挂载点/网卡静态信息等）。
func buildDetail() map[string]any {
	d := map[string]any{"arch": runtime.GOARCH}
	if hi, err := host.Info(); err == nil {
		d["hostname"] = hi.Hostname
		d["kernel"] = hi.KernelVersion
		d["platform"] = hi.Platform
		d["boot_time"] = hi.BootTime
	}
	if ci, err := cpu.Info(); err == nil && len(ci) > 0 {
		d["cpu_model"] = ci[0].ModelName
	}
	if c, err := cpu.Counts(true); err == nil {
		d["cpu_cores"] = c
	}
	mounts := []map[string]any{}
	if parts, err := disk.Partitions(false); err == nil {
		for _, p := range parts {
			if u, err := disk.Usage(p.Mountpoint); err == nil && u.Total > 0 {
				mounts = append(mounts, map[string]any{
					"device": p.Device, "mountpoint": p.Mountpoint, "fstype": p.Fstype,
					"total": int64(u.Total), "used": int64(u.Used), "pct": round1(u.UsedPercent),
				})
			}
		}
	}
	d["mounts"] = mounts
	ifaces := []map[string]any{}
	if list, err := net.Interfaces(); err == nil {
		for _, iface := range list {
			ipv4 := ""
			for _, a := range iface.Addrs {
				if v := ipv4Of(a.Addr); v != "" {
					ipv4 = v
					break
				}
			}
			ifaces = append(ifaces, map[string]any{
				"name": iface.Name, "mac": iface.HardwareAddr, "ipv4": ipv4,
			})
		}
	}
	d["ifaces"] = ifaces
	return d
}

// buildHardware 硬件清单（详情页「硬件详情」按钮展示；静态数据低频刷新）。
func buildHardware() map[string]any {
	hw := map[string]any{}
	// CPU
	cpuInfo := map[string]any{}
	if ci, err := cpu.Info(); err == nil && len(ci) > 0 {
		cpuInfo["model"] = ci[0].ModelName
		cpuInfo["mhz"] = ci[0].Mhz
	}
	if n, err := cpu.Counts(true); err == nil {
		cpuInfo["cores_logical"] = n
	}
	if n, err := cpu.Counts(false); err == nil {
		cpuInfo["cores_physical"] = n
	}
	cpuInfo["arch"] = runtime.GOARCH
	hw["cpu"] = cpuInfo
	// 内存
	if vm, err := mem.VirtualMemory(); err == nil {
		hw["memory"] = map[string]any{
			"total":      int64(vm.Total),
			"swap_total": int64(vm.SwapTotal),
		}
	}
	// 主板 / BIOS（Linux DMI 最佳努力；其他平台留空）
	hw["board"] = readDMI()
	// 磁盘（含分区容量，model 尽力读取）
	disks := []map[string]any{}
	if parts, err := disk.Partitions(false); err == nil {
		for _, p := range parts {
			if u, err := disk.Usage(p.Mountpoint); err == nil && u.Total > 0 {
				disks = append(disks, map[string]any{
					"name": p.Device, "fstype": p.Fstype, "mountpoint": p.Mountpoint,
					"model": diskModel(p.Device),
					"total": int64(u.Total), "used": int64(u.Used), "pct": round1(u.UsedPercent),
				})
			}
		}
	}
	hw["disks"] = disks
	// 网卡
	nics := []map[string]any{}
	if list, err := net.Interfaces(); err == nil {
		for _, iface := range list {
			ipv4 := ""
			for _, a := range iface.Addrs {
				if v := ipv4Of(a.Addr); v != "" {
					ipv4 = v
					break
				}
			}
			if iface.HardwareAddr != "" || ipv4 != "" {
				nics = append(nics, map[string]any{
					"name": iface.Name, "mac": iface.HardwareAddr, "ipv4": ipv4,
				})
			}
		}
	}
	hw["nics"] = nics
	// 系统
	if hi, err := host.Info(); err == nil {
		hw["system"] = map[string]any{
			"hostname": hi.Hostname, "platform": hi.Platform,
			"kernel": hi.KernelVersion, "os": hi.OS,
		}
	}
	return hw
}

// readDMI Linux 下读取 /sys/class/dmi/id；其他平台返回空 map。
func readDMI() map[string]string {
	out := map[string]string{}
	if runtime.GOOS != "linux" {
		return out
	}
	for key, path := range map[string]string{
		"vendor": "/sys/class/dmi/id/sys_vendor", "product": "/sys/class/dmi/id/product_name",
		"serial": "/sys/class/dmi/id/product_serial", "bios": "/sys/class/dmi/id/bios_version",
		"bios_date": "/sys/class/dmi/id/bios_date",
	} {
		if b, err := os.ReadFile(path); err == nil {
			out[key] = strings.TrimSpace(string(b))
		}
	}
	return out
}

// diskModel 尽力读取磁盘型号（Linux /sys/block；其他平台返回空串）。
func diskModel(device string) string {
	if runtime.GOOS != "linux" {
		return ""
	}
	dev := device
	if i := strings.LastIndexByte(dev, '/'); i >= 0 {
		dev = dev[i+1:]
	}
	dev = strings.TrimRight(dev, "0123456789")
	if dev == "" {
		return ""
	}
	if b, err := os.ReadFile("/sys/block/" + dev + "/device/model"); err == nil {
		return strings.TrimSpace(string(b))
	}
	return ""
}

// ---------- 工具 ----------

// ipv4Of 从 "ip/mask" 或 "ip" 字符串中取 IPv4 文本（非 IPv4 返回空串）。
func ipv4Of(addr string) string {
	ip, _, err := stdnet.ParseCIDR(addr)
	if err != nil {
		ip = stdnet.ParseIP(addr)
	}
	if ip != nil && ip.To4() != nil {
		return ip.String()
	}
	return ""
}

func avgVal(a *load.AvgStat, i int) float64 {
	if a == nil {
		return 0
	}
	switch i {
	case 0:
		return round1(a.Load1)
	case 1:
		return round1(a.Load5)
	default:
		return round1(a.Load15)
	}
}

func timesTotal(t cpu.TimesStat) float64 {
	return t.User + t.System + t.Nice + t.Idle + t.Iowait + t.Irq + t.Softirq + t.Steal
}

func timesBusy(t cpu.TimesStat) float64 {
	return timesTotal(t) - t.Idle - t.Iowait
}

func timesBusyPct(t cpu.TimesStat) float64 {
	if timesTotal(t) <= 0 {
		return 0
	}
	return timesBusy(t) / timesTotal(t) * 100
}

func clampPct(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func round1(v float64) float64 {
	return float64(int64(v*10+0.5)) / 10
}

// round2 保留 2 位小数（CPU 百分比精度要求）。
func round2(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}

// buildPayload 组装上报体（push 上报与 listen /metrics 共用）。
func buildPayload(core, detail, live, hardware map[string]any) ([]byte, error) {
	payload := map[string]any{
		"cpu": core["cpu"], "mem_used": core["mem_used"], "mem_total": core["mem_total"],
		"disk_pct": core["disk_pct"], "net_rx": core["net_rx"], "net_tx": core["net_tx"],
		"load1": core["load1"], "uptime": core["uptime"],
		"disk_read": core["disk_read"], "disk_write": core["disk_write"], "per_cpu": core["per_cpu"],
		"detail": detail, "live": live, "hardware": hardware,
		// 身份元数据：版本/目标平台（程序池匹配、可升级提示、OTA）
		"agent_version": version, "agent_goos": runtime.GOOS, "agent_arch": targetArch(),
		// 自监控：Agent 自身内存占用（字节），用于排查 Agent 是否吃内存
		"agent_mem": selfMemBytes(),
	}
	if ip := getPublicIP(); ip != "" {
		payload["public_ip"] = ip
	}
	if evs := bootEventsSnapshot(); len(evs) > 0 {
		payload["boot_events"] = evs
	}
	return json.Marshal(payload)
}

// selfMemBytes 返回 Agent 自身占用的内存（字节，取 Go 运行时的 Sys：向 OS 申请的总量）。
// 用于面板展示「Agent 自身吃了多少内存」，排查采集端是否成为负担。
func selfMemBytes() uint64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.Sys
}

// postPayload POST 指标到 Master（反向模式），返回服务端附带的可选升级指令。
func postPayload(master, token string, body []byte) (*upgradeInfo, error) {
	req, err := http.NewRequest(http.MethodPost,
		strings.TrimRight(master, "/")+"/api/v1/report", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CC-Node-Token", token)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		var m struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &m)
		if m.Message != "" {
			return nil, fmt.Errorf("服务端返回 %d: %s", resp.StatusCode, m.Message)
		}
		return nil, fmt.Errorf("服务端返回 %d", resp.StatusCode)
	}
	// 反代/静态托管可能对错误路径返回 200 + HTML，非 JSON 一律视为失败，防止假成功
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "json") {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return nil, fmt.Errorf("响应不是 JSON（Content-Type=%q），请检查 master 地址: %s",
			ct, strings.TrimSpace(string(raw)))
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var r struct {
		Upgrade *upgradeInfo `json:"upgrade"`
	}
	_ = json.Unmarshal(raw, &r)
	return r.Upgrade, nil
}
