// SSH 采集：远程命令执行与 Linux / Windows 输出解析。
package collector

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"cc_dashboard/server/internal/model"
)

// linuxCmd POSIX 目标机采集脚本（cat /proc + df，输出分段标记）。
// PERCPU 每核、TEMP 传感器、PROC 进程 Top（busybox 兼容回退）。
const linuxCmd = `echo '##CPU##'
grep '^cpu ' /proc/stat
echo '##PERCPU##'
grep -E '^cpu[0-9]+' /proc/stat
echo '##MEM##'
grep -E '^(MemTotal|MemAvailable|MemFree|Buffers|Cached|SwapTotal|SwapFree)' /proc/meminfo
echo '##LOAD##'
cat /proc/loadavg
echo '##UP##'
cat /proc/uptime
echo '##NET##'
cat /proc/net/dev
echo '##DF##'
df -kP / 2>/dev/null | tail -1
echo '##TEMP##'
for z in /sys/class/thermal/thermal_zone*; do [ -r "$z/temp" ] && echo "$(cat "$z/type" 2>/dev/null) $(cat "$z/temp" 2>/dev/null)"; done
echo '##PROC##'
out=$(ps -eo pid=,comm=,pcpu=,pmem= 2>/dev/null | head -6); if [ -n "$out" ]; then echo "$out"; else ps aux 2>/dev/null | head -6; fi
echo '##HOST##'
uname -n
uname -srm`

// windowsPS Windows 目标机采集脚本（输出单行 JSON）。
// per_cpu 每核 + procs 进程 Top5（Win32_PerfFormattedData 性能计数器）。
const windowsPS = `$ErrorActionPreference='SilentlyContinue'
$o=Get-CimInstance Win32_OperatingSystem
$la=@(Get-CimInstance Win32_Processor | Select-Object -ExpandProperty LoadPercentage | Where-Object { $_ -ne $null })
$cpu=0; if($la.Count -gt 0){ $cpu=[int](($la | Measure-Object -Average).Average) }
$dl=@(Get-CimInstance Win32_LogicalDisk -Filter 'DriveType=3')
$pct=0
foreach($d in $dl){ if($d.Size -gt 0){ $p=100-$d.FreeSpace/$d.Size*100; if($p -gt $pct){$pct=$p} } }
$cores=@(Get-CimInstance Win32_PerfFormattedData_PerfOS_Processor -ErrorAction SilentlyContinue | Where-Object { $_.Name -ne '_Total' } | ForEach-Object { [math]::Round([double]$_.PercentProcessorTime,1) })
$procs=@(Get-CimInstance Win32_PerfFormattedData_PerfProc_Process -ErrorAction SilentlyContinue | Where-Object { $_.Name -notin @('_Total','Idle') -and [int]$_.IDProcess -gt 0 } | Sort-Object PercentProcessorTime -Descending | Select-Object -First 5 | ForEach-Object {
  $mp=0; if($o.TotalVisibleMemorySize -gt 0){ $mp=[math]::Round([double]$_.WorkingSet/($o.TotalVisibleMemorySize*1024)*100,1) }
  $cp=[math]::Round([double]$_.PercentProcessorTime,1); if($cp -gt 100){ $cp=100 }
  [pscustomobject]@{ pid=[int]$_.IDProcess; name=[string]$_.Name; cpu=$cp; mem=$mp }
})
$obj=[pscustomobject]@{
  cpu=$cpu
  mem_used=[int64](($o.TotalVisibleMemorySize-$o.FreePhysicalMemory)*1024)
  mem_total=[int64]($o.TotalVisibleMemorySize*1024)
  disk_pct=[math]::Round($pct,2)
  uptime=[int64]((Get-Date)-$o.LastBootUpTime).TotalSeconds
  hostname=$env:COMPUTERNAME
  per_cpu=@($cores)
  procs=@($procs)
}
$obj | ConvertTo-Json -Compress -Depth 4`

func (m *Manager) runLinux(client *ssh.Client, n *model.Node) (*model.MetricsSample, map[string]any, map[string]any, error) {
	out, err := exec(client, linuxCmd)
	if err != nil {
		return nil, nil, nil, err
	}
	return m.parseLinux(n.ID, out, time.Now())
}

func (m *Manager) runWindows(client *ssh.Client, n *model.Node) (*model.MetricsSample, map[string]any, map[string]any, error) {
	// -EncodedCommand 规避引号转义问题（UTF-16LE → Base64）
	enc := base64.StdEncoding.EncodeToString(utf16le(windowsPS))
	out, err := exec(client, `powershell -NoProfile -NonInteractive -EncodedCommand `+enc)
	if err != nil {
		return nil, nil, nil, err
	}
	return parseWindows(out, time.Now())
}

func exec(client *ssh.Client, cmd string) (string, error) {
	sess, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer sess.Close()
	out, err := sess.CombinedOutput(cmd)
	return string(out), err
}

// parseLinux 解析 linuxCmd 输出；CPU/网络使用跨轮差分（本机状态保存在 Manager）。
// 返回 core 指标 + detail + live 实时明细（每核/温度/进程）。
func (m *Manager) parseLinux(nodeID int64, out string, now time.Time) (*model.MetricsSample, map[string]any, map[string]any, error) {
	secs := splitSections(out)
	s := &model.MetricsSample{Timestamp: now.Unix()}

	// CPU 差分
	if line := firstLine(secs["CPU"]); line != "" {
		fields := strings.Fields(line) // cpu user nice system idle iowait irq softirq steal
		if len(fields) >= 5 {
			var vals []float64
			for _, f := range fields[1:] {
				v, _ := strconv.ParseFloat(f, 64)
				vals = append(vals, v)
			}
			total := sum(vals)
			idle := vals[3]
			if len(vals) > 4 {
				idle += vals[4] // + iowait
			}
			m.mu.Lock()
			prev, has := m.prevCPU[nodeID]
			m.prevCPU[nodeID] = cpuSample{total: total, idle: idle, ts: now}
			m.mu.Unlock()
			if has {
				dTotal := total - prev.total
				dIdle := idle - prev.idle
				if dTotal > 0 {
					s.CPU = clampPct((dTotal - dIdle) / dTotal * 100)
				}
			} else if total > 0 {
				s.CPU = clampPct((total - idle) / total * 100) // 首轮：开机以来均值
			}
		}
	}

	// 每核差分（/proc/stat cpuN 行）
	perCPU := m.diffPerCores(nodeID, secs["PERCPU"], now)
	s.PerCPU = perCPU // 进历史表

	// 内存
	mem := map[string]int64{}
	for _, ln := range lines(secs["MEM"]) {
		parts := strings.Fields(ln)
		if len(parts) < 2 {
			continue
		}
		key := strings.TrimSuffix(parts[0], ":")
		v, _ := strconv.ParseInt(parts[1], 10, 64)
		mem[key] = v * 1024 // kB → B
	}
	s.MemTotal = mem["MemTotal"]
	s.MemUsed = s.MemTotal - mem["MemAvailable"]

	// 负载 / 运行时长
	load5, load15 := 0.0, 0.0
	if f := strings.Fields(firstLine(secs["LOAD"])); len(f) >= 3 {
		s.Load1, _ = strconv.ParseFloat(f[0], 64)
		load5, _ = strconv.ParseFloat(f[1], 64)
		load15, _ = strconv.ParseFloat(f[2], 64)
	}
	if f := strings.Fields(firstLine(secs["UP"])); len(f) >= 1 {
		uf, _ := strconv.ParseFloat(f[0], 64)
		s.Uptime = int64(uf)
	}

	// 磁盘占用（df -kP 最后一行 Capacity）
	if ln := firstLine(secs["DF"]); ln != "" {
		f := strings.Fields(ln)
		if len(f) >= 5 {
			cap := strings.TrimSuffix(f[4], "%")
			s.DiskPct, _ = strconv.ParseFloat(cap, 64)
		}
	}

	// 网络差分
	var rxTotal, txTotal uint64
	for _, ln := range lines(secs["NET"]) {
		i := strings.IndexByte(ln, ':')
		if i < 0 {
			continue
		}
		name := strings.TrimSpace(ln[:i])
		if name == "lo" || strings.HasPrefix(name, "|") || strings.HasPrefix(name, "Inter") {
			continue
		}
		f := strings.Fields(ln[i+1:])
		if len(f) < 10 {
			continue
		}
		rx, _ := strconv.ParseUint(f[0], 10, 64)
		tx, _ := strconv.ParseUint(f[8], 10, 64)
		rxTotal += rx
		txTotal += tx
	}
	m.mu.Lock()
	prevNet, hasNet := m.prevNet[nodeID]
	m.prevNet[nodeID] = netSample{rx: rxTotal, tx: txTotal, ts: now}
	m.mu.Unlock()
	if hasNet {
		el := now.Sub(prevNet.ts).Seconds()
		if el > 0 && rxTotal >= prevNet.rx && txTotal >= prevNet.tx {
			s.NetRx = int64(float64(rxTotal-prevNet.rx) / el)
			s.NetTx = int64(float64(txTotal-prevNet.tx) / el)
		}
	}

	// 主机信息
	hostLines := lines(secs["HOST"])
	hostname, kernel := "", ""
	if len(hostLines) >= 1 {
		hostname = strings.TrimSpace(hostLines[0])
	}
	if len(hostLines) >= 2 {
		kernel = strings.TrimSpace(hostLines[1])
	}
	detail := map[string]any{
		"hostname": hostname, "kernel": kernel, "platform": "linux",
		"mounts": []map[string]any{},
	}

	memPct := 0.0
	if s.MemTotal > 0 {
		memPct = clampPct(float64(s.MemUsed) / float64(s.MemTotal) * 100)
	}
	live := map[string]any{
		"ts": now.Unix(), "cpu_total": s.CPU, "per_cpu": perCPU,
		"load1": s.Load1, "load5": load5, "load15": load15,
		"mem": map[string]any{
			"total": s.MemTotal, "used": s.MemUsed,
			"available": mem["MemAvailable"], "free": mem["MemFree"],
			"buffers": mem["Buffers"], "cached": mem["Cached"],
			"swap_total": mem["SwapTotal"], "swap_used": mem["SwapTotal"] - mem["SwapFree"],
			"percent": memPct,
		},
		"temps": parseLinuxTemps(secs["TEMP"]),
		"procs": parseLinuxProcs(secs["PROC"]),
	}
	return s, detail, live, nil
}

// diffPerCores 每核 /proc/stat 行差分 → []float64 使用率。
func (m *Manager) diffPerCores(nodeID int64, sec string, now time.Time) []float64 {
	rows := lines(sec)
	if len(rows) == 0 {
		return []float64{}
	}
	cur := make([]cpuSample, 0, len(rows))
	for _, ln := range rows {
		fields := strings.Fields(ln) // cpuN user nice system idle ...
		if len(fields) < 5 || !strings.HasPrefix(fields[0], "cpu") || fields[0] == "cpu" {
			continue
		}
		var vals []float64
		for _, f := range fields[1:] {
			v, _ := strconv.ParseFloat(f, 64)
			vals = append(vals, v)
		}
		total := sum(vals)
		idle := vals[3]
		if len(vals) > 4 {
			idle += vals[4]
		}
		cur = append(cur, cpuSample{total: total, idle: idle, ts: now})
	}
	if len(cur) == 0 {
		return []float64{}
	}
	m.mu.Lock()
	prev := m.prevPerCores[nodeID]
	m.prevPerCores[nodeID] = cur
	m.mu.Unlock()
	out := make([]float64, len(cur))
	for i := range cur {
		if len(prev) == len(cur) {
			dTotal := cur[i].total - prev[i].total
			dIdle := cur[i].idle - prev[i].idle
			if dTotal > 0 {
				out[i] = clampPct((dTotal - dIdle) / dTotal * 100)
				continue
			}
		}
		if cur[i].total > 0 {
			out[i] = clampPct((cur[i].total - cur[i].idle) / cur[i].total * 100) // 首轮
		}
	}
	return out
}

// parseLinuxTemps thermal_zone 段 → [{label, value°C}]。
// 行格式：<type> <毫摄氏度>，如 "x86_pkg_temp 45000"。
func parseLinuxTemps(sec string) []map[string]any {
	out := []map[string]any{}
	for _, ln := range lines(sec) {
		f := strings.Fields(ln)
		if len(f) < 2 {
			continue
		}
		milli, err := strconv.ParseInt(f[len(f)-1], 10, 64)
		if err != nil || milli <= 0 || milli > 200000 {
			continue // 非毫摄氏度数值（>200°C 或非数字）跳过
		}
		label := strings.Join(f[:len(f)-1], " ")
		out = append(out, map[string]any{
			"label": label, "value": float64(int64(float64(milli)/100*10)) / 100, // milli→°C 保留1位
		})
	}
	return out
}

// parseLinuxProcs 进程 Top 段 → [{pid,name,cpu,mem}]。
// 兼容两种输出：procps "pid comm pcpu pmem"（无表头）与 busybox "ps aux"（含 USER 表头）。
func parseLinuxProcs(sec string) []map[string]any {
	out := []map[string]any{}
	rows := lines(sec)
	if len(rows) == 0 {
		return out
	}
	aux := len(rows) > 0 && strings.Fields(rows[0])[0] == "USER"
	for _, ln := range rows {
		f := strings.Fields(ln)
		if aux {
			// USER PID %CPU %MEM VSZ RSS TTY STAT START TIME COMMAND...
			if len(f) < 5 || f[0] == "USER" {
				continue
			}
			pid, err1 := strconv.ParseInt(f[1], 10, 64)
			cpu, err2 := strconv.ParseFloat(f[2], 64)
			memP, err3 := strconv.ParseFloat(f[3], 64)
			if err1 != nil || err2 != nil || err3 != nil {
				continue
			}
			name := f[0]
			if len(f) >= 11 {
				name = strings.Join(f[10:], " ")
			}
			out = append(out, map[string]any{"pid": pid, "name": name, "cpu": cpu, "mem": memP})
		} else {
			// pid comm pcpu pmem
			if len(f) < 4 {
				continue
			}
			pid, err1 := strconv.ParseInt(f[0], 10, 64)
			cpu, err2 := strconv.ParseFloat(f[2], 64)
			memP, err3 := strconv.ParseFloat(f[3], 64)
			if err1 != nil || err2 != nil || err3 != nil {
				continue
			}
			out = append(out, map[string]any{"pid": pid, "name": f[1], "cpu": cpu, "mem": memP})
		}
		if len(out) >= 5 {
			break
		}
	}
	return out
}

// parseWindows 解析 windowsPS 输出的 JSON（每核 + 进程 Top5 进 live）。
func parseWindows(out string, now time.Time) (*model.MetricsSample, map[string]any, map[string]any, error) {
	body := extractJSON(out)
	if body == "" {
		return nil, nil, nil, fmt.Errorf("windows 输出无 JSON: %s", truncate(out, 200))
	}
	var w struct {
		CPU      float64 `json:"cpu"`
		MemUsed  int64   `json:"mem_used"`
		MemTotal int64   `json:"mem_total"`
		DiskPct  float64 `json:"disk_pct"`
		Uptime   int64   `json:"uptime"`
		Hostname string  `json:"hostname"`
		// 容错：Windows PowerShell 5.1 会把单元素数组序列化成标量 → any 接收后再规范
		PerCPU any `json:"per_cpu"`
		Procs  any `json:"procs"`
	}
	if err := json.Unmarshal([]byte(body), &w); err != nil {
		return nil, nil, nil, fmt.Errorf("windows JSON 解析失败: %w", err)
	}
	s := &model.MetricsSample{
		Timestamp: now.Unix(), CPU: w.CPU, MemUsed: w.MemUsed, MemTotal: w.MemTotal,
		DiskPct: w.DiskPct, Uptime: w.Uptime,
	}
	detail := map[string]any{
		"hostname": w.Hostname, "platform": "windows",
		"mounts": []map[string]any{},
	}
	memPct := 0.0
	if w.MemTotal > 0 {
		memPct = clampPct(float64(w.MemUsed) / float64(w.MemTotal) * 100)
	}
	live := map[string]any{
		"ts": now.Unix(), "cpu_total": w.CPU,
		"per_cpu": anyToFloatSlice(w.PerCPU),		"mem": map[string]any{
			"total": w.MemTotal, "used": w.MemUsed, "percent": memPct,
			"available": w.MemTotal - w.MemUsed, "free": w.MemTotal - w.MemUsed,
			"swap_total": int64(0), "swap_used": int64(0), "buffers": int64(0), "cached": int64(0),
		},
		"temps": []map[string]any{}, // Windows 无通用温度 API
		"procs": anyToProcSlice(w.Procs),
	}
	s.PerCPU = live["per_cpu"].([]float64) // 进历史表
	return s, detail, live, nil
}

// anyToFloatSlice 将 JSON 反序列化结果规范为 []float64（标量包装为单元素数组）。
func anyToFloatSlice(v any) []float64 {
	out := []float64{}
	switch t := v.(type) {
	case []any:
		for _, e := range t {
			switch n := e.(type) {
			case float64:
				out = append(out, n)
			}
		}
	case float64:
		out = append(out, t)
	}
	return out
}

// anyToProcSlice 将进程列表规范为 []map[string]any（容忍标量/类型偏差）。
func anyToProcSlice(v any) []map[string]any {
	out := []map[string]any{}
	appendOne := func(e any) {
		m, ok := e.(map[string]any)
		if !ok {
			return
		}
		out = append(out, map[string]any{
			"pid":  toInt64(m["pid"]),
			"name": toString(m["name"]),
			"cpu":  toFloat(m["cpu"]),
			"mem":  toFloat(m["mem"]),
		})
	}
	switch t := v.(type) {
	case []any:
		for _, e := range t {
			appendOne(e)
		}
	case map[string]any:
		appendOne(t)
	}
	return out
}

func toInt64(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case json.Number:
		i, _ := n.Int64()
		return i
	}
	return 0
}

func toFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	}
	return 0
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// ---------- 工具 ----------

func splitSections(out string) map[string]string {
	res := map[string]string{}
	var cur string
	var buf []string
	for _, ln := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(ln)
		if strings.HasPrefix(trimmed, "##") && strings.HasSuffix(trimmed, "##") {
			if cur != "" {
				res[cur] = strings.Join(buf, "\n")
			}
			cur = strings.Trim(trimmed, "#")
			buf = nil
			continue
		}
		if cur != "" {
			buf = append(buf, ln)
		}
	}
	if cur != "" {
		res[cur] = strings.Join(buf, "\n")
	}
	return res
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func lines(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return strings.Split(strings.TrimSpace(s), "\n")
}

func sum(v []float64) float64 {
	t := 0.0
	for _, x := range v {
		t += x
	}
	return t
}

func clampPct(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return float64(int64(v*100+0.5)) / 100 // 保留 2 位
}

func extractJSON(out string) string {
	i := strings.IndexByte(out, '{')
	if i < 0 {
		return ""
	}
	j := strings.LastIndexByte(out, '}')
	if j < i {
		return ""
	}
	return out[i : j+1]
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func utf16le(s string) []byte {
	b := make([]byte, 0, len(s)*2)
	for _, r := range s {
		b = binary.LittleEndian.AppendUint16(b, uint16(r))
	}
	return b
}

func marshalJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}
