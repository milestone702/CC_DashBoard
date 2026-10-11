// ESXi 采集（模式 B 变体）：SSH 执行 esxcli + esxtop 并解析。
// 真机校准（2026-10，ESXi 8 @ 实测）：
//   - uptime：esxcli 返回微秒（≥1e9 按微秒换算）
//   - esxtop -b 为 PDH-CSV：CPU%/每核/负载/内存已用（经典表格回退）
//   - 存储：esxcli storage filesystem list → 挂载点明细 + 主数据store占用
//   - 网络：nic list（MAC/速率/链路）+ nic stats get（Bytes 累计差分→吞吐）+ vmk IP
package collector

import (
	"encoding/csv"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"cc_dashboard/server/internal/model"
)

// esxiCmd ESXi Shell 采集脚本（单会话，esxtop 批量约 2~4 秒）。
const esxiCmd = `echo '##HOST##'
hostname
echo '##UP##'
esxcli system stats uptime get
echo '##CPU##'
esxcli hardware cpu list | grep -c 'CPU ID:'
echo '##MEM##'
esxcli hardware memory get
echo '##FS##'
esxcli storage filesystem list
echo '##NICS##'
esxcli network nic list
echo '##IP4##'
esxcli network ip interface ipv4 get
echo '##IFACE2##'
esxcli network ip interface list
echo '##NICTX##'
for n in $(esxcli network nic list | awk 'NR>2{print $1}'); do echo "--$n--"; esxcli network nic stats get -n $n; done
echo '##DISKIO##'
esxcli storage core device stats get
echo '##TEMP##'
esxcli hardware sensor temperature get
echo '##ESXTOP##'
esxtop -b -d 2 -n 1`

type esxiNetSample struct {
	rx, tx int64
	ts     time.Time
}

// esxiDiskSample 单设备磁盘累计 I/O 命令数（差分用）。
type esxiDiskSample struct {
	read, write int64
	ts          time.Time
}

func (m *Manager) runESXI(client *ssh.Client, n *model.Node) (*model.MetricsSample, map[string]any, map[string]any, error) {
	out, err := exec(client, esxiCmd)
	if err != nil {
		return nil, nil, nil, err
	}
	return m.parseESXI(n.ID, out, time.Now())
}

// parseESXI 解析组合输出：指标 + 详情（挂载/网卡）+ 实时明细（每核/分网卡）。
func (m *Manager) parseESXI(nodeID int64, out string, now time.Time) (*model.MetricsSample, map[string]any, map[string]any, error) {
	secs := splitSections(out)
	if secs["ESXTOP"] == "" && secs["UP"] == "" {
		return nil, nil, nil, fmt.Errorf("esxi 输出缺少数据段: %s", truncate(out, 200))
	}
	s := &model.MetricsSample{Timestamp: now.Unix()}
	s.Uptime = normalizeUptime(firstInt(secs["UP"]))
	s.MemTotal = parsePhysicalMem(secs["MEM"])
	ncpuFB := firstInt(secs["CPU"])
	hostname := firstLine(secs["HOST"])

	cpuPct, memUsed, load1, _, perCPU := parseEsxtopFlexible(secs["ESXTOP"], s.MemTotal, ncpuFB)
	s.CPU = cpuPct
	s.MemUsed = memUsed
	s.Load1 = load1
	s.PerCPU = perCPU // 进历史表
	if s.MemTotal > 0 && memUsed == 0 {
		// CSV 未给出内存时的退路：无法估算，保持 0（面板按 0 显示）
		_ = memUsed
	}

	// 存储：挂载明细 + 主数据store占用
	mounts, diskPct := parseFS(secs["FS"])
	s.DiskPct = diskPct

	// 网络：每网卡 Bytes 累计 → 差分速率；合计进 sample
	nicStats := parseNICTX(secs["NICTX"])
	m.mu.Lock()
	prev, hasPrev := m.prevESXI[nodeID]
	nowT := now
	var curRx, curTx int64
	m.mu.Unlock()
	for _, v := range nicStats {
		curRx += v.rx
		curTx += v.tx
	}
	m.mu.Lock()
	m.prevESXI[nodeID] = esxiNetSample{rx: curRx, tx: curTx, ts: now}
	m.mu.Unlock()
	if hasPrev && nowT.Sub(prev.ts).Seconds() > 0 && curRx >= prev.rx && curTx >= prev.tx {
		el := nowT.Sub(prev.ts).Seconds()
		s.NetRx = int64(float64(curRx-prev.rx) / el)
		s.NetTx = int64(float64(curTx-prev.tx) / el)
	}

	// 网卡静态信息 + 速率 + vmk IP（按 MAC 关联）
	nics := parseNICList(secs["NICS"])
	vmkMAC := parseIfaceMAC(secs["IFACE2"])
	vmkIP := parseIP4(secs["IP4"])
	mac2ip := map[string]string{}
	for vmk, mac := range vmkMAC {
		if ip, ok := vmkIP[vmk]; ok && mac != "" {
			mac2ip[mac] = ip
		}
	}
	liveNics := []map[string]any{}
	ifaces := []map[string]any{}
	for _, nic := range nics {
		ip := mac2ip[nic["mac"]]
		ifaces = append(ifaces, map[string]any{
			"name": nic["name"], "mac": nic["mac"], "ipv4": ip, "speed": nic["speed"],
		})
		liveNics = append(liveNics, map[string]any{
			"name": nic["name"], "mac": nic["mac"], "ipv4": ip,
			"rx_bps": int64(0), "tx_bps": int64(0),
		})
	}
	// 单网卡速率（独立状态）
	perNicRate := m.nicRates(nodeID, nicStats, now)
	for i := range liveNics {
		if r, ok := perNicRate[liveNics[i]["name"].(string)]; ok {
			liveNics[i]["rx_bps"] = r[0]
			liveNics[i]["tx_bps"] = r[1]
		}
	}

	memPct := 0.0
	if s.MemTotal > 0 {
		memPct = clampPct(float64(s.MemUsed) / float64(s.MemTotal) * 100)
	}

	// 磁盘 I/O：esxcli 累计命令数差分 → IOPS；esxtop CSV 宽松匹配 wait → 延迟
	diskIO := map[string]any{"read_bps": int64(0), "write_bps": int64(0)}
	if rates, totR, totW := m.esxiDiskRates(nodeID, parseESXIDiskIO(secs["DISKIO"]), now); len(rates) > 0 || totR > 0 || totW > 0 {
		diskIO["read_iops"] = totR
		diskIO["write_iops"] = totW
		diskIO["devices"] = rates
	}
	if lat := parseDiskWaitMS(secs["ESXTOP"]); lat > 0 {
		diskIO["lat_ms"] = lat
	}

	detail := map[string]any{
		"hostname": hostname, "platform": "esxi", "arch": "x86_64",
		"mounts": mounts, "ifaces": ifaces,
	}
	live := map[string]any{
		"ts": now.Unix(), "cpu_total": s.CPU, "per_cpu": perCPU, "load1": s.Load1,
		"mem": map[string]any{
			"total": s.MemTotal, "used": s.MemUsed, "percent": memPct,
			"available": 0, "free": s.MemTotal - s.MemUsed,
			"swap_total": 0, "swap_used": 0, "buffers": 0, "cached": 0,
		},
		"nics": liveNics,
		"disk_io": diskIO,
		"temps": parseESXITemps(secs["TEMP"]),
		"procs": []map[string]any{}, // ESXi 无稳定进程 API（esxtop world 信息格式随版本变化）
	}
	return s, detail, live, nil
}

// parseESXIDiskIO esxcli storage core device stats get → {device: {read, write 累计命令数}}。
// 输出：无缩进设备行（naa./t10./mpx. 开头）+ 缩进的 "Read: N" / "Write: N" 计数行。
func parseESXIDiskIO(text string) map[string]esxiDiskSample {
	out := map[string]esxiDiskSample{}
	cur := ""
	for _, ln := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(ln)
		if trimmed == "" {
			continue
		}
		if !strings.HasPrefix(ln, " ") && !strings.HasPrefix(ln, "\t") {
			// 无缩进 = 新设备行（首字段为设备标识）
			f := strings.Fields(trimmed)
			if len(f) >= 1 && (strings.HasPrefix(f[0], "naa.") || strings.HasPrefix(f[0], "t10.") ||
				strings.HasPrefix(f[0], "mpx.") || strings.HasPrefix(f[0], "vmhba")) {
				cur = f[0]
				if _, ok := out[cur]; !ok {
					out[cur] = esxiDiskSample{}
				}
			} else {
				cur = ""
			}
			continue
		}
		if cur == "" {
			continue
		}
		f := strings.Fields(trimmed)
		if len(f) < 2 {
			continue
		}
		v, err := strconv.ParseInt(strings.TrimSuffix(f[1], ","), 10, 64)
		if err != nil || v < 0 {
			continue
		}
		s := out[cur]
		switch strings.TrimSuffix(f[0], ":") {
		case "Read":
			s.read = v
		case "Write":
			s.write = v
		}
		out[cur] = s
	}
	return out
}

// esxiDiskRates 单设备累计差分 → IOPS；返回 ({device: [read_iops, write_iops]}, 合计读, 合计写)。
func (m *Manager) esxiDiskRates(nodeID int64, cur map[string]esxiDiskSample, now time.Time) (map[string][2]int64, int64, int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.prevESXIDisk == nil {
		m.prevESXIDisk = map[int64]map[string]esxiDiskSample{}
	}
	prev := m.prevESXIDisk[nodeID]
	out := map[string][2]int64{}
	var totR, totW int64
	el := 0.0
	if !prevEmpty(prev) {
		if t, ok := firstDiskTS(prev); ok {
			el = now.Sub(t).Seconds()
		}
	}
	snap := map[string]esxiDiskSample{}
	for name, v := range cur {
		v.ts = now
		snap[name] = v
		if p, ok := prev[name]; ok && el > 0 && v.read >= p.read && v.write >= p.write {
			r := int64(float64(v.read-p.read) / el)
			w := int64(float64(v.write-p.write) / el)
			out[name] = [2]int64{r, w}
			totR += r
			totW += w
		}
	}
	m.prevESXIDisk[nodeID] = snap
	return out, totR, totW
}

func prevEmpty(m map[string]esxiDiskSample) bool { return len(m) == 0 }

func firstDiskTS(m map[string]esxiDiskSample) (time.Time, bool) {
	for _, v := range m {
		if !v.ts.IsZero() {
			return v.ts, true
		}
	}
	return time.Time{}, false
}

// parseDiskWaitMS 从 esxtop PDH-CSV 宽松匹配磁盘等待/延迟列（毫秒）。
// 列名含 Disk 且含 wait|latency|service time 之一；无匹配返回 0。
func parseDiskWaitMS(text string) float64 {
	if !strings.Contains(text, "PDH-CSV") {
		return 0
	}
	reader := csv.NewReader(strings.NewReader(text))
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	records, err := reader.ReadAll()
	if err != nil && err != io.EOF {
		return 0
	}
	if len(records) < 2 {
		return 0
	}
	header := records[0]
	row := records[len(records)-1]
	for i, h := range header {
		if i >= len(row) {
			break
		}
		hl := strings.ToLower(h)
		if !strings.Contains(hl, "disk") {
			continue
		}
		if !strings.Contains(hl, "wait") && !strings.Contains(hl, "latency") && !strings.Contains(hl, "service time") {
			continue
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(row[i]), 64)
		if err != nil || v <= 0 || v > 60000 {
			continue
		}
		return float64(int64(v*100+0.5)) / 100
	}
	return 0
}

// parseESXITemps esxcli hardware sensor temperature get → [{label, value°C}]。
// 输出为空格对齐表（Key 列可能含空格，如 "CPU1 temSENSOR"）→ 扫描首个 0~150 的
// 数值字段作为读数，其前为名称、其后为单位；单位为 V/RPM/A 等非温度量纲则跳过。
func parseESXITemps(sec string) []map[string]any {
	out := []map[string]any{}
	for _, ln := range lines(sec) {
		f := strings.Fields(ln)
		if len(f) < 2 || f[0] == "Key" || strings.HasPrefix(f[0], "---") {
			continue
		}
		// 找数值字段
		vi := -1
		var v float64
		for i := 1; i < len(f); i++ {
			if x, err := strconv.ParseFloat(strings.TrimSuffix(f[i], ","), 64); err == nil && x >= 0 && x <= 150 {
				vi, v = i, x
				break
			}
		}
		if vi < 0 {
			continue
		}
		// 单位字段存在且明显非温度 → 跳过（电压/风扇转速等）
		if vi+1 < len(f) {
			unit := strings.ToLower(f[vi+1])
			switch unit {
			case "v", "mv", "a", "ma", "rpm", "w", "hz":
				continue
			}
		}
		label := strings.Join(f[:vi], " ")
		if label == "" {
			continue
		}
		dup := false
		for _, e := range out {
			if e["label"] == label {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		out = append(out, map[string]any{"label": label, "value": v})
		if len(out) >= 16 {
			break
		}
	}
	return out
}

// nicRates 单网卡差分。
func (m *Manager) nicRates(nodeID int64, cur map[string]esxiNetSample, now time.Time) map[string][2]int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.prevESXINic == nil {
		m.prevESXINic = map[int64]map[string]esxiNetSample{}
	}
	if m.prevESXINicTS == nil {
		m.prevESXINicTS = map[int64]time.Time{}
	}
	prev := m.prevESXINic[nodeID]
	out := map[string][2]int64{}
	el := 0.0
	if !m.prevESXINicTS[nodeID].IsZero() {
		el = now.Sub(m.prevESXINicTS[nodeID]).Seconds()
	}
	snap := map[string]esxiNetSample{}
	for name, v := range cur {
		snap[name] = v
		if el > 0 {
			if p, ok := prev[name]; ok && v.rx >= p.rx && v.tx >= p.tx {
				out[name] = [2]int64{
					int64(float64(v.rx-p.rx) / el),
					int64(float64(v.tx-p.tx) / el),
				}
			}
		}
	}
	m.prevESXINic[nodeID] = snap
	m.prevESXINicTS[nodeID] = now
	return out
}

// normalizeUptime 微秒/秒自适应。
func normalizeUptime(v int64) int64 {
	if v >= 1_000_000_000 {
		return v / 1_000_000
	}
	return v
}

// ---------- esxtop CSV / 经典 ----------

func parseEsxtopFlexible(text string, phys int64, ncpuFB int64) (cpuPct float64, memUsed int64, load1 float64, ncpu int64, perCPU []float64) {
	if strings.Contains(text, "PDH-CSV") {
		cpuPct, memUsed, load1, ncpu, perCPU = parseEsxtopCSV(text, phys)
		if ncpu == 0 {
			ncpu = ncpuFB
		}
		return
	}
	cpu, mem := parseEsxtopClassic(text, phys, ncpuFB)
	return cpu, mem, 0, ncpuFB, nil
}

func parseEsxtopCSV(text string, phys int64) (cpuPct float64, memUsed int64, load1 float64, ncpu int64, perCPU []float64) {
	reader := csv.NewReader(strings.NewReader(text))
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	records, err := reader.ReadAll()
	if err != nil && err != io.EOF {
		return
	}
	if len(records) < 2 {
		return
	}
	header := records[0]
	var row []string
	for i := len(records) - 1; i >= 1; i-- {
		if len(records[i]) == len(header) {
			row = records[i]
			break
		}
	}
	if row == nil {
		return
	}

	var loadPct, machineMB, freeMB float64
	var totalUtil, totalProc float64
	var hasUtilTotal, hasProcTotal, hasLoad, hasMachine, hasFree bool
	utilMap := map[int]float64{}
	procMap := map[int]float64{}
	for i, h := range header {
		if i >= len(row) {
			break
		}
		h = strings.TrimSpace(h)
		val, err := strconv.ParseFloat(strings.TrimSpace(row[i]), 64)
		if err != nil {
			continue
		}
		switch {
		// 真机列名形如 `Physical Cpu(_Total)\% Util Time`（含反斜杠转义），用 Contains+Suffix 组合匹配
		case strings.Contains(h, `Physical Cpu(_Total)`) && strings.HasSuffix(h, `% Util Time`):
			totalUtil, hasUtilTotal = val, true
		case strings.Contains(h, `Physical Cpu(_Total)`) && strings.HasSuffix(h, `% Processor Time`):
			totalProc, hasProcTotal = val, true
		case strings.HasSuffix(h, `Cpu Load (1 Minute Avg)`):
			loadPct, hasLoad = val, true
		case strings.HasSuffix(h, `Memory\Machine MBytes`):
			machineMB, hasMachine = val, true
		case strings.HasSuffix(h, `Memory\Free MBytes`):
			freeMB, hasFree = val, true
		default:
			if idx, isUtil, ok := perCPUIndex(h); ok {
				if isUtil {
					utilMap[idx] = val
				} else {
					procMap[idx] = val
				}
			}
		}
	}
	// CPU 总值优先 Util Time（= ESXi 界面「CPU 使用率」口径，真机 8.94≈9%），退 Processor Time
	switch {
	case hasUtilTotal:
		cpuPct = totalUtil
	case hasProcTotal:
		cpuPct = totalProc
	case hasLoad:
		cpuPct = loadPct
	}
	// 每核同样优先 Util（真机 7~12% 与面板口径一致）
	perMap := utilMap
	if len(perMap) == 0 {
		perMap = procMap
	}
	ncpu = int64(len(perMap))
	if len(perMap) > 0 {
		max := 0
		for k := range perMap {
			if k > max {
				max = k
			}
		}
		perCPU = make([]float64, 0, max+1)
		for i := 0; i <= max; i++ {
			perCPU = append(perCPU, clampPct(perMap[i]))
		}
	}
	cpuPct = clampPct(cpuPct)
	if hasMachine && hasFree && machineMB > freeMB {
		memUsed = int64((machineMB - freeMB) * 1024 * 1024)
	}
	if phys > 0 && memUsed > phys {
		memUsed = phys
	}
	// Cpu Load 真机为 0~1 比例（0.10=10%）；个别版本为百分比 → 按幅度自适应
	if hasLoad && ncpu > 0 {
		if loadPct <= 1.5 {
			load1 = loadPct * float64(ncpu) // 比例×核数 ≈ 负载
		} else {
			load1 = loadPct / 100 * float64(ncpu)
		}
		load1 = float64(int64(load1*100+0.5)) / 100
	}
	return
}

// perCPUIndex 识别单核计数器：`Physical Cpu(N)\% Util Time` 或 `...\% Processor Time`。
// 返回 (核序号, 是否Util, 是否命中)。
func perCPUIndex(h string) (int, bool, bool) {
	if !strings.Contains(h, `Physical Cpu(`) {
		return 0, false, false
	}
	isUtil := strings.HasSuffix(h, `% Util Time`)
	isProc := strings.HasSuffix(h, `% Processor Time`)
	if !isUtil && !isProc {
		return 0, false, false
	}
	start := strings.Index(h, `Physical Cpu(`) + len(`Physical Cpu(`)
	end := strings.Index(h[start:], ")")
	if end <= 0 {
		return 0, false, false
	}
	digit := h[start : start+end]
	if digit == "_Total" {
		return 0, false, false
	}
	n, err := strconv.Atoi(digit)
	if err != nil {
		return 0, false, false
	}
	return n, isUtil, true
}

func parseEsxtopClassic(text string, phys int64, ncpu int64) (cpuPct float64, memUsed int64) {
	var section string
	var header []string
	idxUsed, idxMB := -1, -1
	var sumUSED, sumMB float64
	for _, ln := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(ln)
		if trimmed == "" {
			header = nil
			continue
		}
		upper := strings.ToUpper(trimmed)
		switch {
		case strings.HasPrefix(upper, "CPU:"):
			section, header = "CPU", nil
			continue
		case strings.HasPrefix(upper, "MEM:"):
			section, header = "MEM", nil
			continue
		case strings.HasPrefix(upper, "DISK"), strings.HasPrefix(upper, "NET"), strings.HasPrefix(upper, "UI:"):
			section, header = "", nil
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) == 0 {
			continue
		}
		if fields[0] == "worldID" || fields[0] == "ID" {
			header = fields
			idxUsed, idxMB = -1, -1
			for i, h := range header {
				if h == "%USED" {
					idxUsed = i
				}
				if h == "MB" {
					idxMB = i
				}
			}
			continue
		}
		if header == nil {
			continue
		}
		wid, err := strconv.Atoi(fields[0])
		if err != nil || wid == 0 {
			continue
		}
		if section == "CPU" && idxUsed >= 0 && idxUsed < len(fields) {
			if v, err := strconv.ParseFloat(fields[idxUsed], 64); err == nil {
				sumUSED += v
			}
		}
		if section == "MEM" && idxMB >= 0 && idxMB < len(fields) {
			if v, err := strconv.ParseFloat(fields[idxMB], 64); err == nil {
				sumMB += v
			}
		}
	}
	_ = phys
	if ncpu > 0 && sumUSED > 0 {
		cpuPct = clampPct(sumUSED / float64(ncpu))
	}
	if sumMB > 0 {
		memUsed = int64(sumMB * 1024 * 1024)
	}
	return cpuPct, memUsed
}

// ---------- esxcli 解析 ----------

var fsLineRe = regexp.MustCompile(`^(\S+)\s+(.+?)\s+(\S+)\s+(true|false)\s+(\S+)\s+(\d+)\s+(\d+)$`)

// parseFS 解析 storage filesystem list → 挂载明细 + 主数据store占用%（最大 VMFS 卷）。
func parseFS(text string) (mounts []map[string]any, diskPct float64) {
	mounts = []map[string]any{}
	var maxSize, maxUsed int64
	for _, ln := range strings.Split(text, "\n") {
		ln = strings.TrimRight(ln, "\r")
		if strings.HasPrefix(ln, "Mount Point") || strings.HasPrefix(ln, "---") || strings.TrimSpace(ln) == "" {
			continue
		}
		m := fsLineRe.FindStringSubmatch(ln)
		if m == nil {
			continue
		}
		size, _ := strconv.ParseInt(m[6], 10, 64)
		free, _ := strconv.ParseInt(m[7], 10, 64)
		if size <= 0 {
			continue
		}
		used := size - free
		pct := clampPct(float64(used) / float64(size) * 100)
		mounts = append(mounts, map[string]any{
			"device": m[2], "mountpoint": m[1], "fstype": m[5],
			"total": size, "used": used, "pct": pct,
		})
		if m[5] != "vfat" && size > maxSize {
			maxSize, maxUsed = size, used
		}
	}
	if maxSize > 0 {
		diskPct = clampPct(float64(maxUsed) / float64(maxSize) * 100)
	}
	return mounts, diskPct
}

// parseNICList 解析 nic list → [{name, mac, speed, link}]。
func parseNICList(text string) []map[string]string {
	out := []map[string]string{}
	for _, ln := range strings.Split(text, "\n") {
		fields := strings.Fields(ln)
		if len(fields) < 9 || !strings.HasPrefix(fields[0], "vmnic") {
			continue
		}
		// Name PCI Driver Admin Link Speed Duplex MAC MTU ...
		out = append(out, map[string]string{
			"name": fields[0], "mac": fields[7], "speed": fields[5], "link": fields[4],
		})
	}
	return out
}

// parseNICTX 解析 nic stats get 分块 → {vmnic: {rx,tx}}（累计字节）。
func parseNICTX(text string) map[string]esxiNetSample {
	out := map[string]esxiNetSample{}
	cur := ""
	for _, ln := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(ln)
		if strings.HasPrefix(trimmed, "--") && strings.HasSuffix(trimmed, "--") {
			cur = strings.Trim(trimmed, "-")
			continue
		}
		if cur == "" {
			continue
		}
		if v, ok := suffixInt(trimmed, "Bytes received:"); ok {
			s := out[cur]
			s.rx = v
			out[cur] = s
		}
		if v, ok := suffixInt(trimmed, "Bytes sent:"); ok {
			s := out[cur]
			s.tx = v
			out[cur] = s
		}
	}
	return out
}

func suffixInt(line, key string) (int64, bool) {
	idx := strings.Index(line, key)
	if idx < 0 {
		return 0, false
	}
	num := strings.TrimSpace(line[idx+len(key):])
	v, err := strconv.ParseInt(num, 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// parseIP4 ipv4 get → {vmk0: ip}。
func parseIP4(text string) map[string]string {
	out := map[string]string{}
	for _, ln := range strings.Split(text, "\n") {
		fields := strings.Fields(ln)
		if len(fields) >= 2 && strings.HasPrefix(fields[0], "vmk") && strings.Contains(fields[1], ".") {
			out[fields[0]] = fields[1]
		}
	}
	return out
}

// parseIfaceMAC interface list → {vmk0: mac}。
func parseIfaceMAC(text string) map[string]string {
	out := map[string]string{}
	cur := ""
	for _, ln := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(ln)
		if strings.HasPrefix(trimmed, "vmk") && !strings.Contains(trimmed, ":") {
			cur = trimmed
			continue
		}
		if strings.HasPrefix(trimmed, "Name:") {
			fields := strings.Fields(trimmed)
			if len(fields) >= 2 {
				cur = fields[1]
			}
			continue
		}
		if strings.HasPrefix(trimmed, "MAC Address:") && cur != "" {
			fields := strings.Fields(trimmed)
			if len(fields) >= 3 {
				out[cur] = fields[2]
			}
		}
	}
	return out
}

// parsePhysicalMem 解析 "Physical Memory: <n> [bytes|kB|MB|GB]"。
func parsePhysicalMem(s string) int64 {
	for _, ln := range lines(s) {
		low := strings.ToLower(ln)
		if !strings.Contains(low, "physical memory") && !strings.Contains(low, "physical ram") {
			continue
		}
		fields := strings.Fields(strings.TrimRight(ln, ":"))
		for i, f := range fields {
			v, err := strconv.ParseFloat(strings.Trim(f, ","), 64)
			if err != nil {
				continue
			}
			unit := "bytes"
			if i+1 < len(fields) {
				unit = strings.ToLower(fields[i+1])
			}
			switch unit {
			case "kb", "kib":
				return int64(v * 1024)
			case "mb", "mib":
				return int64(v * 1024 * 1024)
			case "gb", "gib":
				return int64(v * 1024 * 1024 * 1024)
			default:
				return int64(v)
			}
		}
	}
	return 0
}

// firstInt 文本中第一个非负整数。
func firstInt(s string) int64 {
	for _, f := range strings.Fields(s) {
		f = strings.Trim(f, ",")
		if v, err := strconv.ParseInt(f, 10, 64); err == nil && v >= 0 {
			return v
		}
	}
	return 0
}
