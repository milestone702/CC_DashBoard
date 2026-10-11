// 群晖 Synology DSM Web API 采集（模式 C 变体，os_type=nas + access_mode=api）。
// 认证：SYNO.API.Auth 登录换取 SID（缓存复用，失效自动重登）
// 指标：SYNO.Core.System.Utilization（CPU/内存/网络/磁盘 I/O）+ SYNO.Core.System info（uptime/温度/机型）
//       + SYNO.Core.Network get（主机名 server_name）
// 真机校准（2026-10-10，DSM 7.2.2 实测）：
//   - CPU：user_load/system_load/other_load 为百分比（无 user/system/idle 字段）；1/5/15min_load 为负载均值
//   - 内存：total_real/avail_real/buffer/cached 为 KB；real_usage 为百分比（含缓存回收口径）
//   - 网络：rx/tx 为**瞬时速率 B/s**（非累计字节！）；device=total 为各接口求和项，不可重复累加
//   - 磁盘：disk.total read/write_byte 为速率 B/s、read/write_access 为 IOPS、utilization 为繁忙度%
//   - 存储占用：space.total.utilization（%）；DSM6 的 user/system/idle 与 total/used/free 字段仍兼容
package collector

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"cc_dashboard/server/internal/model"
)

type synoSID struct {
	sid string
	ts  time.Time
}

// synoHTTPClient DSM 常为自签证书。
var synoHTTPClient = &http.Client{
	Timeout: 10 * time.Second,
	Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // DSM 自签证书
	},
}

// synoLogin SYNO.API.Auth 登录（version=3 兼容 DSM 6/7）。
func synoLogin(client *http.Client, base, account, passwd string) (string, error) {
	u := base + "/webapi/auth.cgi?api=SYNO.API.Auth&version=3&method=login" +
		"&account=" + url.QueryEscape(account) +
		"&passwd=" + url.QueryEscape(passwd) +
		"&format=sid"
	var wrap struct {
		Success bool `json:"success"`
		Code    int  `json:"code"`
		Data    struct {
			SID string `json:"sid"`
		} `json:"data"`
	}
	body, err := httpGet(client, u)
	if err != nil {
		return "", err
	}
	if err := json.Unmarshal(body, &wrap); err != nil {
		return "", fmt.Errorf("登录响应解析失败: %w", err)
	}
	if !wrap.Success || wrap.Data.SID == "" {
		return "", fmt.Errorf("登录失败 code=%d", wrap.Code)
	}
	return wrap.Data.SID, nil
}

// synoUtilization SYNO.Core.System.Utilization 响应（DSM7 真机字段 + DSM6 兼容）。
type synoUtilization struct {
	Success bool   `json:"success"`
	Code    int    `json:"code"`
	Data    struct {
		CPU struct {
			// DSM 7：百分比负载 + 负载均值
			UserLoad   float64 `json:"user_load"`
			SystemLoad float64 `json:"system_load"`
			OtherLoad  float64 `json:"other_load"`
			Load1      float64 `json:"1min_load"`
			Load5      float64 `json:"5min_load"`
			Load15     float64 `json:"15min_load"`
			// DSM 6 兼容
			User   float64 `json:"user"`
			System float64 `json:"system"`
			Idle   float64 `json:"idle"`
		} `json:"cpu"`
		Memory struct {
			TotalReal float64 `json:"total_real"` // KB
			AvailReal float64 `json:"avail_real"` // KB（不含 buffer/cached）
			Buffer    float64 `json:"buffer"`     // KB
			Cached    float64 `json:"cached"`     // KB
			RealUsage float64 `json:"real_usage"` // %
			TotalSwap float64 `json:"total_swap"` // KB
			AvailSwap float64 `json:"avail_swap"` // KB
			SwapUsage float64 `json:"swap_usage"` // %
			// DSM 6 兼容（数值尺度自适应）
			Total float64 `json:"total"`
			Used  float64 `json:"used"`
			Free  float64 `json:"free"`
		} `json:"memory"`
		Network []struct {
			Device string  `json:"device"`
			Rx     float64 `json:"rx"` // B/s（瞬时速率）
			Tx     float64 `json:"tx"`
		} `json:"network"`
		Disk struct {
			Disk []struct {
				Device      string  `json:"device"`
				DisplayName string  `json:"display_name"`
				ReadByte    float64 `json:"read_byte"`  // B/s
				WriteByte   float64 `json:"write_byte"` // B/s
				ReadAccess  float64 `json:"read_access"`  // 次/s
				WriteAccess float64 `json:"write_access"` // 次/s
				Utilization float64 `json:"utilization"`  // %
			} `json:"disk"`
			Total struct {
				ReadByte    float64 `json:"read_byte"`
				WriteByte   float64 `json:"write_byte"`
				ReadAccess  float64 `json:"read_access"`
				WriteAccess float64 `json:"write_access"`
				Utilization float64 `json:"utilization"`
			} `json:"total"`
		} `json:"disk"`
		Space struct {
			Total struct {
				ReadByte    float64 `json:"read_byte"`
				WriteByte   float64 `json:"write_byte"`
				Utilization float64 `json:"utilization"` // 存储占用 %
			} `json:"total"`
			Volume []struct {
				Device      string  `json:"device"`
				DisplayName string  `json:"display_name"`
				Utilization float64 `json:"utilization"`
			} `json:"volume"`
		} `json:"space"`
	} `json:"data"`
}

// synoSystemInfo SYNO.Core.System method=info 响应（getinfo 在 DSM7.2 已下线，info 才是有效方法）。
type synoSystemInfo struct {
	Success bool `json:"success"`
	Code    int  `json:"code"`
	Data    struct {
		Uptime      string  `json:"up_time"`      // "584:47:0"（时:分:秒）
		SysTemp     float64 `json:"sys_temp"`     // °C；虚拟机/无传感器为 0
		Model       string  `json:"model"`        // 如 DS3617xs
		CPUFamily   string  `json:"cpu_family"`   // 如 Core(TM)
		CPUSeries   string  `json:"cpu_series"`   // 如 i3-N305
		CPUCores    string  `json:"cpu_cores"`    // 如 4C/4T
		CPUClock    int     `json:"cpu_clock_speed"`
		RAMSize     int     `json:"ram_size"` // MB
		FirmwareVer string  `json:"firmware_ver"`
	} `json:"data"`
}

// synoProcess SYNO.Core.System.Process list 单条（dsm_helper 项目验证的接口）。
// cpu 为 0.1% 粒度（12 → 1.2%）；mem / mem_shared 为 KB。
type synoProcess struct {
	PID       int64   `json:"pid"`
	Command   string  `json:"command"`
	CPU       float64 `json:"cpu"`
	Mem       float64 `json:"mem"`        // KB（含共享）
	MemShared float64 `json:"mem_shared"` // KB
	Status    string  `json:"status"`
}

// synoStorage SYNO.Storage.CGI.Storage load_info 响应（容量口径，非 space.utilization 繁忙度）。
// 注意：size.total/used 为字符串数字（真机实测）。
type synoStorage struct {
	Success bool `json:"success"`
	Code    int  `json:"code"`
	Data    struct {
		Volumes []struct {
			ID      string `json:"id"`      // volume_1
			FsType  string `json:"fs_type"` // ext4
			VolPath string `json:"vol_path"` // /volume1
			Size    struct {
				Total string `json:"total"` // 字节（字符串）
				Used  string `json:"used"`
			} `json:"size"`
			Status string `json:"status"`
		} `json:"volumes"`
	} `json:"data"`
}

// volBytes 解析 size 字符串字段（空/非法返回 0）。
func volBytes(s string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || v < 0 {
		return 0
	}
	return v
}

// pct 汇总全部卷的容量占用（%）；无有效卷返回 (0,false)。
func (s *synoStorage) pct() (float64, bool) {
	var total, used float64
	for _, v := range s.Data.Volumes {
		t, u := volBytes(v.Size.Total), volBytes(v.Size.Used)
		if t <= 0 {
			continue
		}
		total += t
		used += u
	}
	if total <= 0 {
		return 0, false
	}
	return clampPct(used / total * 100), true
}

// pullSynology 拉取 DSM 状态并入库。
func (m *Manager) pullSynology(n *model.Node) {
	creds, err := m.decryptCreds(n)
	if err != nil {
		log.Printf("SYNO[%s] 凭据解密失败: %v", n.Name, err)
		return
	}
	if creds.Username == "" {
		log.Printf("SYNO[%s] 未配置 DSM 账号", n.Name)
		return
	}
	port := n.Port
	if port <= 0 {
		port = 5001 // DSM HTTPS 默认端口
	}
	scheme := "https"
	if port == 5000 {
		scheme = "http"
	}
	base := fmt.Sprintf("%s://%s:%d", scheme, n.Address, port)

	// SID 缓存（2 小时内复用），失败重登一次
	sid := m.getSID(n.ID, base, creds.Username, creds.Password)
	if sid == "" {
		log.Printf("SYNO[%s] 登录失败", n.Name)
		return
	}
	util, err := fetchSynoUtilization(synoHTTPClient, base, sid)
	if err != nil {
		m.invalidateSID(n.ID)
		sid, err = synoLogin(synoHTTPClient, base, creds.Username, creds.Password)
		if err != nil || sid == "" {
			log.Printf("SYNO[%s] 重登失败: %v", n.Name, err)
			return
		}
		m.setSID(n.ID, sid)
		util, err = fetchSynoUtilization(synoHTTPClient, base, sid)
		if err != nil {
			log.Printf("SYNO[%s] 采集失败: %v", n.Name, err)
			return
		}
	}
	info, _ := fetchSynoSystem(synoHTTPClient, base, sid)
	procs, _ := fetchSynoProcesses(synoHTTPClient, base, sid) // 进程 Top5（失败不阻断）
	storage, _ := fetchSynoStorage(synoHTTPClient, base, sid) // 容量口径（失败沿用 space 兜底）

	now := time.Now()
	sample := m.synoSample(util, info, now)
	if storage != nil {
		if pct, ok := storage.pct(); ok {
			sample.DiskPct = pct // 卷容量占用（space.utilization 实测为 I/O 繁忙度，非容量）
		}
	}

	detail := map[string]any{
		"platform": "synology",
		"hostname": m.synoServerName(n.ID, base, sid),
		"mounts":   []map[string]any{},
	}
	if storage != nil {
		mounts := []map[string]any{}
		for _, v := range storage.Data.Volumes {
			total, used := volBytes(v.Size.Total), volBytes(v.Size.Used)
			if total <= 0 {
				continue
			}
			mounts = append(mounts, map[string]any{
				"device": v.ID, "mountpoint": orSyno(v.VolPath, "/"+v.ID),
				"fstype": v.FsType, "total": int64(total), "used": int64(used),
				"pct": clampPct(used / total * 100),
			})
		}
		detail["mounts"] = mounts
	}
	if info != nil && info.Success {
		cpuModel := strings.TrimSpace(info.Data.CPUSeries)
		if info.Data.CPUFamily != "" {
			cpuModel = strings.TrimSpace(info.Data.CPUFamily + " " + cpuModel)
		}
		if cpuModel != "" {
			detail["cpu_model"] = cpuModel
		}
		if info.Data.CPUCores != "" {
			detail["cpu_cores"] = info.Data.CPUCores
		}
		if info.Data.FirmwareVer != "" {
			detail["firmware"] = info.Data.FirmwareVer
		}
		if info.Data.Model != "" {
			detail["model"] = info.Data.Model
		}
		detail["arch"] = "x86_64"
	}
	detailJSON, _ := marshalJSON(detail)
	live := synoLive(sample, util, info, procs, now)
	liveJSON, _ := marshalJSON(live)
	m.ingest(n, sample, detailJSON, "", json.RawMessage(liveJSON))
}

// synoSample 组装样本（真机校准口径见文件头注释）。
// prevSYNO 差分已移除：DSM7 网络/磁盘均为瞬时速率，直接采用。
func (m *Manager) synoSample(u *synoUtilization, info *synoSystemInfo, now time.Time) *model.MetricsSample {
	s := &model.MetricsSample{Timestamp: now.Unix()}

	// CPU：DSM6 走 user/system/idle；DSM7 走 *_load 百分比
	dsm6 := u.Data.CPU.Idle > 0 || u.Data.CPU.User > 0 || u.Data.CPU.System > 0
	if dsm6 {
		if busy := u.Data.CPU.User + u.Data.CPU.System; busy > 0 && busy <= 100 {
			s.CPU = clampPct(busy)
		} else if u.Data.CPU.Idle > 0 {
			s.CPU = clampPct(100 - u.Data.CPU.Idle)
		}
	} else {
		// DSM7：user+system+other（other 含 iowait 等），空闲机实测 ≈3%
		s.CPU = clampPct(u.Data.CPU.UserLoad + u.Data.CPU.SystemLoad + u.Data.CPU.OtherLoad)
		s.Load1 = round2(u.Data.CPU.Load1)
		// load5/load15 只进 live（MetricsSample 无此列）
	}

	// 内存
	if u.Data.Memory.TotalReal > 0 {
		// DSM7：KB → 字节；used = total - avail - buffer - cached（与 real_usage 口径吻合）
		total := u.Data.Memory.TotalReal
		used := total - u.Data.Memory.AvailReal - u.Data.Memory.Buffer - u.Data.Memory.Cached
		if used < 0 {
			used = 0
		}
		s.MemTotal = int64(total * 1024)
		s.MemUsed = int64(used * 1024)
	} else if u.Data.Memory.Total > 0 {
		// DSM6：数值尺度自适应（<1e7 视为 MB，否则按字节）
		memTotal := u.Data.Memory.Total
		if memTotal < 10_000_000 {
			memTotal *= 1024 * 1024
		}
		memUsed := u.Data.Memory.Used
		if memUsed > 0 && memUsed < 10_000_000 {
			memUsed *= 1024 * 1024
		}
		if memUsed == 0 && u.Data.Memory.Free > 0 {
			free := u.Data.Memory.Free
			if free < 10_000_000 {
				free *= 1024 * 1024
			}
			memUsed = memTotal - free
		}
		s.MemTotal = int64(memTotal)
		s.MemUsed = int64(memUsed)
	}

	// 运行时长：up_time "584:47:0"（时:分:秒）
	if info != nil {
		s.Uptime = parseSynoUpTime(info.Data.Uptime)
	}

	// 网络：瞬时速率 B/s（device=total 为求和项；缺失时自行求和且排除 total/lo 防双计）
	rx, tx, hasTotal := 0.0, 0.0, false
	var sumRx, sumTx float64
	for _, nic := range u.Data.Network {
		if nic.Device == "total" {
			rx, tx, hasTotal = nic.Rx, nic.Tx, true
			continue
		}
		if nic.Device == "lo" {
			continue
		}
		sumRx += nic.Rx
		sumTx += nic.Tx
	}
	if !hasTotal {
		rx, tx = sumRx, sumTx
	}
	s.NetRx = int64(rx)
	s.NetTx = int64(tx)

	// 磁盘 I/O（历史列）与存储占用（space.utilization）
	s.DiskRead = int64(u.Data.Disk.Total.ReadByte)
	s.DiskWrite = int64(u.Data.Disk.Total.WriteByte)
	s.DiskPct = clampPct(u.Data.Space.Total.Utilization)

	return s
}

// synoLive 实时明细：分网卡速率 / 磁盘 I/O / 温度 / 进程 Top5。
// per_cpu 留空：DSM Utilization 无每核字段。
func synoLive(s *model.MetricsSample, u *synoUtilization, info *synoSystemInfo, procs []synoProcess, now time.Time) map[string]any {
	nics := []map[string]any{}
	for _, nic := range u.Data.Network {
		if nic.Device == "total" || nic.Device == "lo" {
			continue
		}
		nics = append(nics, map[string]any{
			"name": nic.Device, "ipv4": "",
			"rx_bps": int64(nic.Rx), "tx_bps": int64(nic.Tx),
		})
	}

	memPct := 0.0
	if u.Data.Memory.RealUsage > 0 {
		memPct = clampPct(u.Data.Memory.RealUsage)
	} else if s.MemTotal > 0 {
		memPct = clampPct(float64(s.MemUsed) / float64(s.MemTotal) * 100)
	}

	temps := []map[string]any{}
	if info != nil && info.Data.SysTemp > 0 {
		temps = append(temps, map[string]any{"label": "系统", "value": info.Data.SysTemp})
	}

	// 进程 Top5：按 cpu（0.1% 粒度）倒序；mem% 基于样本总内存
	top := procs
	if len(top) > 5 {
		top = top[:5] // fetchSynoProcesses 已排序
	}
	liveProcs := []map[string]any{}
	for _, p := range top {
		memP := 0.0
		if s.MemTotal > 0 && p.Mem > 0 {
			memP = clampPct(p.Mem * 1024 / float64(s.MemTotal) * 100)
		}
		liveProcs = append(liveProcs, map[string]any{
			"pid": p.PID, "name": p.Command,
			"cpu": float64(int64(p.CPU/10*10+0.5)) / 10, // 0.1% 粒度 → %
			"mem": memP,
		})
	}

	return map[string]any{
		"ts": now.Unix(),
		"cpu_total": s.CPU,
		"per_cpu": []float64{},
		"load1": s.Load1, "load5": round2(u.Data.CPU.Load5), "load15": round2(u.Data.CPU.Load15),
		"mem": map[string]any{
			"total": s.MemTotal, "used": s.MemUsed, "percent": memPct,
			"available": int64(u.Data.Memory.AvailReal * 1024),
			"free":      int64(u.Data.Memory.AvailReal * 1024),
			"buffers":   int64(u.Data.Memory.Buffer * 1024),
			"cached":    int64(u.Data.Memory.Cached * 1024),
			"swap_total": int64(u.Data.Memory.TotalSwap * 1024),
			"swap_used":  int64((u.Data.Memory.TotalSwap - u.Data.Memory.AvailSwap) * 1024),
		},
		"nics": nics,
		"disk_io": map[string]any{
			"read_bps": s.DiskRead, "write_bps": s.DiskWrite,
			"read_iops": int64(u.Data.Disk.Total.ReadAccess),
			"write_iops": int64(u.Data.Disk.Total.WriteAccess),
		},
		"temps": temps,
		"procs": liveProcs,
	}
}

// parseSynoUpTime 解析 "584:47:0"（时:分:秒）或纯秒数字符串。
func parseSynoUpTime(v string) int64 {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if n, err := strconv.ParseInt(v, 10, 64); err == nil {
		return n // 纯数字（个别版本直接给秒）
	}
	parts := strings.Split(v, ":")
	if len(parts) != 3 {
		return 0
	}
	h, err1 := strconv.ParseInt(parts[0], 10, 64)
	m, err2 := strconv.ParseInt(parts[1], 10, 64)
	sec, err3 := strconv.ParseInt(parts[2], 10, 64)
	if err1 != nil || err2 != nil || err3 != nil {
		return 0
	}
	return h*3600 + m*60 + sec
}

func fetchSynoUtilization(client *http.Client, base, sid string) (*synoUtilization, error) {
	u := base + "/webapi/entry.cgi?api=SYNO.Core.System.Utilization&version=1&method=get&_sid=" + url.QueryEscape(sid)
	body, err := httpGet(client, u)
	if err != nil {
		return nil, err
	}
	var wrap synoUtilization
	if err := json.Unmarshal(body, &wrap); err != nil {
		return nil, err
	}
	if !wrap.Success {
		return nil, fmt.Errorf("utilization code=%d", wrap.Code)
	}
	return &wrap, nil
}

// fetchSynoSystem method=info（DSM7.2 实测 getinfo 已下线返回 103，info 为有效方法）。
func fetchSynoSystem(client *http.Client, base, sid string) (*synoSystemInfo, error) {
	u := base + "/webapi/entry.cgi?api=SYNO.Core.System&version=1&method=info&_sid=" + url.QueryEscape(sid)
	body, err := httpGet(client, u)
	if err != nil {
		return nil, err
	}
	var wrap synoSystemInfo
	if err := json.Unmarshal(body, &wrap); err != nil {
		return nil, err
	}
	if !wrap.Success {
		return nil, fmt.Errorf("system info code=%d", wrap.Code)
	}
	return &wrap, nil
}

// fetchSynoProcesses SYNO.Core.System.Process method=list（dsm_helper 项目验证），
// 按 cpu 倒序返回（响应实测 566 进程 ≈46KB）。
func fetchSynoProcesses(client *http.Client, base, sid string) ([]synoProcess, error) {
	u := base + "/webapi/entry.cgi?api=SYNO.Core.System.Process&method=list&version=1&_sid=" + url.QueryEscape(sid)
	body, err := httpGet(client, u)
	if err != nil {
		return nil, err
	}
	var wrap struct {
		Success bool `json:"success"`
		Code    int  `json:"code"`
		Data    struct {
			Process []synoProcess `json:"process"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &wrap); err != nil {
		return nil, err
	}
	if !wrap.Success {
		return nil, fmt.Errorf("process list code=%d", wrap.Code)
	}
	list := wrap.Data.Process
	// 响应可能未排序：按 cpu 倒序、同 cpu 按 mem 倒序取 Top
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && (list[j].CPU > list[j-1].CPU ||
			(list[j].CPU == list[j-1].CPU && list[j].Mem > list[j-1].Mem)); j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
	return list, nil
}

// fetchSynoStorage SYNO.Storage.CGI.Storage method=load_info —— 卷容量口径（实测 ≈16KB）。
func fetchSynoStorage(client *http.Client, base, sid string) (*synoStorage, error) {
	u := base + "/webapi/entry.cgi?api=SYNO.Storage.CGI.Storage&method=load_info&version=1&_sid=" + url.QueryEscape(sid)
	body, err := httpGet(client, u)
	if err != nil {
		return nil, err
	}
	var wrap synoStorage
	if err := json.Unmarshal(body, &wrap); err != nil {
		return nil, err
	}
	if !wrap.Success {
		return nil, fmt.Errorf("storage load_info code=%d", wrap.Code)
	}
	return &wrap, nil
}

// synoServerName 主机名：SYNO.Core.Network method=get → server_name（结果按节点缓存，极少变化）。
func (m *Manager) synoServerName(nodeID int64, base, sid string) string {
	m.mu.Lock()
	if v, ok := m.synoNames[nodeID]; ok && v != "" {
		m.mu.Unlock()
		return v
	}
	m.mu.Unlock()
	u := base + "/webapi/entry.cgi?api=SYNO.Core.Network&version=1&method=get&_sid=" + url.QueryEscape(sid)
	body, err := httpGet(synoHTTPClient, u)
	if err != nil {
		return ""
	}
	var wrap struct {
		Success bool `json:"success"`
		Data    struct {
			ServerName string `json:"server_name"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &wrap) != nil || !wrap.Success {
		return ""
	}
	m.mu.Lock()
	if m.synoNames == nil {
		m.synoNames = map[int64]string{}
	}
	m.synoNames[nodeID] = wrap.Data.ServerName
	m.mu.Unlock()
	return wrap.Data.ServerName
}

func httpGet(client *http.Client, rawURL string) ([]byte, error) {
	resp, err := client.Get(rawURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 256<<10))
}

// ---------- SID 缓存 ----------

func (m *Manager) getSID(nodeID int64, base, account, passwd string) string {
	m.mu.Lock()
	s := m.sids[nodeID]
	m.mu.Unlock()
	if s.sid != "" && time.Since(s.ts) < 2*time.Hour {
		return s.sid
	}
	sid, err := synoLogin(synoHTTPClient, base, account, passwd)
	if err != nil {
		return ""
	}
	m.setSID(nodeID, sid)
	return sid
}

func (m *Manager) setSID(nodeID int64, sid string) {
	m.mu.Lock()
	m.sids[nodeID] = synoSID{sid: sid, ts: time.Now()}
	m.mu.Unlock()
}

func (m *Manager) invalidateSID(nodeID int64) {
	m.mu.Lock()
	delete(m.sids, nodeID)
	m.mu.Unlock()
}

func round2(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}

// orSyno 空串回退。
func orSyno(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}
