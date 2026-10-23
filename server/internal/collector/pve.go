// 模式 C：Proxmox VE (PVE) REST API 采集。
// GET /api2/json/nodes/{node}/status   —— 宿主机指标
// GET /api2/json/cluster/resources     —— 虚机/容器汇总（存入 detail.vms）
package collector

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"time"

	"cc_dashboard/server/internal/model"
)

type pveStatus struct {
	CPU     float64   `json:"cpu"`    // 0~1（占全体核心比例）
	Mem     int64     `json:"mem"`    // 已用字节
	MaxMem  int64     `json:"maxmem"` // 总字节
	Uptime  int64     `json:"uptime"`
	NetIn   int64     `json:"netin"`  // 累计字节
	NetOut  int64     `json:"netout"` // 累计字节
	Disk    int64     `json:"disk"`
	MaxDisk int64     `json:"maxdisk"`
	LoadAvg []float64 `json:"loadavg"` // [1m,5m,15m]（live 用）
}

type pveVM struct {
	Vmid   int64   `json:"vmid"`
	Name   string  `json:"name"`
	Status string  `json:"status"` // running / stopped
	Type   string  `json:"type"`   // qemu | lxc
	CPU    float64 `json:"cpu"`
	Mem    int64   `json:"mem"`
	MaxMem int64   `json:"maxmem"`
}

type pveNetSample struct {
	in, out int64
	ts      time.Time
}

// pveHTTPClient 跳过自签证书（PVE 默认自签；局域网定位）。
var pveHTTPClient = &http.Client{
	Timeout: 10 * time.Second,
	Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // PVE 自签证书
	},
}

// scanAPI 扫描 access_mode=api 的节点：按 os_type 分派（pve → PVE API，nas → 群晖 DSM API）。
func (m *Manager) scanAPI(now int64) {
	nodes, err := m.db.ListNodes()
	if err != nil {
		return
	}
	for _, n := range nodes {
		if n.AccessMode != model.ModeAPI || !n.Enabled || !n.Approved {
			continue
		}
		interval := n.ReportInterval
		if interval <= 0 {
			interval = 15
		}
		if n.LastSeen > 0 && now-n.LastSeen < int64(interval)-1 {
			continue
		}
		switch n.OSType {
		case model.OSTypeNAS:
			m.pullSynology(n)
		default: // pve 及未标注类型
			m.pullPVE(n)
		}
	}
}

// pullPVE 拉取 PVE 状态与虚机汇总并入库。
func (m *Manager) pullPVE(n *model.Node) {
	creds, err := m.decryptCreds(n)
	if err != nil {
		log.Printf("PVE[%s] 凭据解密失败: %v", n.Name, err)
		_ = m.db.SetNodeError(n.ID, fmt.Sprintf("凭据解密失败: %v", err))
		return
	}
	if creds.Username == "" || creds.NodeName == "" {
		log.Printf("PVE[%s] 未配置 API Token 标识或节点名", n.Name)
		_ = m.db.SetNodeError(n.ID, "未配置 API Token 标识或节点名")
		return
	}
	port := n.Port
	if port <= 0 {
		port = 8006 // PVE 默认端口
	}
	scheme := "https"
	if port == 80 {
		scheme = "http"
	}
	base := fmt.Sprintf("%s://%s", scheme, net.JoinHostPort(n.Address, fmt.Sprintf("%d", port)))
	auth := "PVEAPIToken=" + creds.Username + "=" + creds.Password

	status, vms, err := fetchPVE(pveHTTPClient, base, auth, creds.NodeName)
	if err != nil {
		log.Printf("PVE[%s] 请求失败: %v", n.Name, err)
		_ = m.db.SetNodeError(n.ID, fmt.Sprintf("PVE API 请求失败: %v", err))
		return
	}

	nowT := time.Now()
	sample := m.pveSample(n.ID, status, nowT)

	detail := map[string]any{
		"hostname": creds.NodeName, "platform": "pve",
		"vms": vms,
	}
	detailJSON, _ := marshalJSON(detail)

	// PVE API 无每核/温度/进程端点 → live 给出可得字段，其余留空数组
	load1, load5, load15 := 0.0, 0.0, 0.0
	if len(status.LoadAvg) > 0 {
		load1 = status.LoadAvg[0]
	}
	if len(status.LoadAvg) > 1 {
		load5 = status.LoadAvg[1]
	}
	if len(status.LoadAvg) > 2 {
		load15 = status.LoadAvg[2]
	}
	memPct := 0.0
	if sample.MemTotal > 0 {
		memPct = clampPct(float64(sample.MemUsed) / float64(sample.MemTotal) * 100)
	}
	live := map[string]any{
		"ts": nowT.Unix(), "cpu_total": sample.CPU,
		"per_cpu": []float64{},
		"load1":   load1, "load5": load5, "load15": load15,
		"mem": map[string]any{
			"total": sample.MemTotal, "used": sample.MemUsed, "percent": memPct,
			"available": sample.MemTotal - sample.MemUsed, "free": sample.MemTotal - sample.MemUsed,
			"swap_total": int64(0), "swap_used": int64(0), "buffers": int64(0), "cached": int64(0),
		},
		"temps": []map[string]any{}, "procs": []map[string]any{}, "nics": []map[string]any{},
	}
	liveJSON, _ := marshalJSON(live)
	m.ingest(n, sample, detailJSON, "", json.RawMessage(liveJSON))
}

// pveSample 组装样本：CPU 百分比、内存、运行时长、网络速率（跨轮差分）。
func (m *Manager) pveSample(nodeID int64, st *pveStatus, now time.Time) *model.MetricsSample {
	s := &model.MetricsSample{
		Timestamp: now.Unix(),
		CPU:       clampPct(st.CPU * 100), // API 返回 0~1 比例
		MemUsed:   st.Mem,
		MemTotal:  st.MaxMem,
		Uptime:    st.Uptime,
	}
	if st.MaxDisk > 0 && st.Disk > 0 {
		s.DiskPct = clampPct(float64(st.Disk) / float64(st.MaxDisk) * 100)
	}

	m.mu.Lock()
	prev, has := m.prevPVE[nodeID]
	m.prevPVE[nodeID] = pveNetSample{in: st.NetIn, out: st.NetOut, ts: now}
	m.mu.Unlock()
	if has && now.Sub(prev.ts).Seconds() > 0 {
		el := now.Sub(prev.ts).Seconds()
		if st.NetIn >= prev.in && st.NetOut >= prev.out {
			s.NetRx = int64(float64(st.NetIn-prev.in) / el)
			s.NetTx = int64(float64(st.NetOut-prev.out) / el)
		}
	}
	return s
}

// fetchPVE 请求状态与虚机资源（独立函数便于单元测试）。
func fetchPVE(client *http.Client, base, auth, nodeName string) (*pveStatus, []map[string]any, error) {
	// 状态
	var stWrap struct {
		Data pveStatus `json:"data"`
	}
	stURL := base + "/api2/json/nodes/" + url.PathEscape(nodeName) + "/status"
	body, err := pveGet(client, stURL, auth)
	if err != nil {
		return nil, nil, err
	}
	if err := json.Unmarshal(body, &stWrap); err != nil {
		return nil, nil, fmt.Errorf("status 解析失败: %w", err)
	}

	// 虚机/容器汇总
	var resWrap struct {
		Data []pveVM `json:"data"`
	}
	resURL := base + "/api2/json/cluster/resources?type=vm"
	vms := []map[string]any{}
	if body, err := pveGet(client, resURL, auth); err == nil {
		if err := json.Unmarshal(body, &resWrap); err == nil {
			for i, vm := range resWrap.Data {
				if i >= 80 {
					break
				}
				vms = append(vms, map[string]any{
					"vmid": vm.Vmid, "name": vm.Name, "status": vm.Status,
					"type": vm.Type, "cpu": vm.CPU,
					"mem": vm.Mem, "maxmem": vm.MaxMem,
				})
			}
		}
	}
	return &stWrap.Data, vms, nil
}

func pveGet(client *http.Client, url, auth string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", auth)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 512<<10))
}
