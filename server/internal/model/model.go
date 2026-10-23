// 领域模型：节点、接入模式与指标样本。
package model

import "encoding/json"

// AccessMode 三大接入模式（开发文档第 2 节）
type AccessMode string

const (
	ModeAgent AccessMode = "agent" // A: Agent 主动上报
	ModeSSH   AccessMode = "ssh"   // B: SSH 远程轮询
	ModeAPI   AccessMode = "api"   // C: Hypervisor 专属 API（PVE / ESXi）
)

// OSType 目标系统类型
type OSType string

const (
	OSTypeLinux   OSType = "linux"
	OSTypeWindows OSType = "windows"
	OSTypeNAS     OSType = "nas"
	OSTypeOpenWrt OSType = "openwrt"
	OSTypeDarwin  OSType = "darwin"
	OSTypePVE     OSType = "pve"
	OSTypeESXi    OSType = "esxi"
)

// 节点状态（存储值 online/offline；列表接口会按告警动态显示 alert）
const (
	StatusOnline  = "online"
	StatusOffline = "offline"
	StatusAlert   = "alert"
)

// Node 监控节点（表 nodes）
type Node struct {
	ID             int64          `json:"id"`
	Name           string         `json:"name"`
	OSType         OSType         `json:"os_type"`
	AccessMode     AccessMode     `json:"access_mode"`
	Address        string         `json:"address"`
	Port           int            `json:"port"`
	Token          string         `json:"token"`
	Enabled        bool           `json:"enabled"`
	Approved       bool           `json:"approved"`        // 入网审批：false=pending，后台批准后才允许上报
	Direction      string         `json:"direction"`       // 连接方向：push(反向，默认) | pull(正向，Server 拉取)
	ReportInterval int            `json:"report_interval"` // 秒，离线判定 = 3×interval
	Status         string         `json:"status"`
	LastSeen       int64          `json:"last_seen"`         // unix 秒，最后一次上报
	CreatedAt      int64          `json:"created_at"`        // unix 秒
	SortOrder      int            `json:"sort_order"`        // 手动排序（管理页上下移，概览同步）
	AuthEnc        string         `json:"-"`                 // AES-256-GCM 加密后的接入凭据
	PublicIP       string         `json:"public_ip"`         // 公网 IP（服务端源 IP 或 Agent 上报）
	AgentVersion   string         `json:"agent_version"`     // Agent 上报的自身版本
	AgentGoos      string         `json:"agent_goos"`        // Agent 目标 OS（runtime.GOOS）
	AgentArch      string         `json:"agent_arch"`        // Agent 目标架构（amd64/arm64/armv7/mipsle）
	GroupTag       string         `json:"group_tag"`         // 分组标签（自由文本，空=未分组）
	MuteUntil      int64          `json:"mute_until"`        // 告警静默截止（unix 秒，0=未静默）
	AgentMem       int64          `json:"agent_mem"`         // Agent 自身内存占用（字节，0=未知）
	LastError      string         `json:"last_error"`        // 最近一次采集/连接失败原因（成功采集后清空）
	LastErrorAt    int64          `json:"last_error_at"`     // 失败时间（unix 秒，0=无）
	Latest         *MetricsSample `json:"latest,omitempty"`  // 列表/详情接口动态填充
	Upgrade        *UpgradeHint   `json:"upgrade,omitempty"` // 池中有可升级版本时动态填充
}

// UpgradeHint 可升级提示（节点接口动态计算）。
type UpgradeHint struct {
	Version string `json:"version"`
	PoolID  int64  `json:"pool_id"`
}

// PoolItem 程序池条目（表 pool_items；文件存 data/pool/ 下）。
type PoolItem struct {
	ID        int64  `json:"id"`
	Component string `json:"component"` // agent | server
	Version   string `json:"version"`
	GOOS      string `json:"goos"`
	Arch      string `json:"arch"`
	Format    string `json:"format"` // bin | deb | rpm | ipk
	Filename  string `json:"filename"`
	SHA256    string `json:"sha256"`
	Size      int64  `json:"size"`
	Note      string `json:"note"`
	CreatedAt int64  `json:"created_at"`
}

// BootEvent 开关机记录（表 boot_events）。
type BootEvent struct {
	TS   int64  `json:"ts"`   // unix 秒
	Type string `json:"type"` // on | off
}

// MetricsSample 单次采样指标：Agent 上报与落盘共用结构。
type MetricsSample struct {
	NodeID    int64     `json:"node_id"`   // 服务端按 Token 覆盖
	Timestamp int64     `json:"ts"`        // 服务端按接收时间覆盖
	CPU       float64   `json:"cpu"`       // 使用率 %
	MemUsed   int64     `json:"mem_used"`  // 字节
	MemTotal  int64     `json:"mem_total"` // 字节
	MemPct    float64   `json:"mem_pct"`   // 内存使用率 %（服务端计算后下发，客户端无需重算）
	DiskPct   float64   `json:"disk_pct"`  // %
	NetRx     int64     `json:"net_rx"`    // Bytes/s
	NetTx     int64     `json:"net_tx"`    // Bytes/s
	Load1     float64   `json:"load1"`
	Uptime    int64     `json:"uptime"`            // 秒
	DiskRead  int64     `json:"disk_read"`         // 磁盘读 Bytes/s（历史曲线）
	DiskWrite int64     `json:"disk_write"`        // 磁盘写 Bytes/s
	TempMax   float64   `json:"temp_max"`          // 最高温度 ℃（服务端从 live.temps 提取，0=无传感器）
	PerCPU    []float64 `json:"per_cpu,omitempty"` // 每核使用率 %（历史落盘）
}

// MemUsedPct 内存使用率（mem_total 为 0 时返回 0）。
func (m *MetricsSample) MemUsedPct() float64 {
	if m == nil || m.MemTotal <= 0 {
		return 0
	}
	return float64(m.MemUsed) / float64(m.MemTotal) * 100
}

// FillMemPct 计算并填充 MemPct（读路径在返回前统一调用）。
func (m *MetricsSample) FillMemPct() {
	if m != nil {
		m.MemPct = m.MemUsedPct()
	}
}

// HistoryPoint 历史查询返回的数据点（与表列一致）
type HistoryPoint struct {
	TS        int64   `json:"ts"`
	CPU       float64 `json:"cpu"`
	MemUsed   int64   `json:"mem_used"`
	MemTotal  int64   `json:"mem_total"`
	MemPct    float64 `json:"mem_pct"` // 服务端计算（mem_total 为 0 时 0）
	DiskPct   float64 `json:"disk_pct"`
	NetRx     int64   `json:"net_rx"`
	NetTx     int64   `json:"net_tx"`
	Load1     float64 `json:"load1"`
	Uptime    int64   `json:"uptime"`
	DiskRead  int64   `json:"disk_read"` // 磁盘 I/O 历史
	DiskWrite int64   `json:"disk_write"`
	TempMax   float64 `json:"temp_max"` // 最高温度 ℃（0=无传感器）
	PerCPU    string  `json:"per_cpu"`  // JSON 数组文本（聚合窗口取一条），空串=无数据
}

// FillMemPct 计算并填充 MemPct。
func (p *HistoryPoint) FillMemPct() {
	if p == nil {
		return
	}
	if p.MemTotal > 0 {
		p.MemPct = float64(p.MemUsed) / float64(p.MemTotal) * 100
	}
}

// ExtractTempMax 从 live 明细里取最高温度（temps[].value，单位 ℃）。
// live 结构由采集端定义（Agent / SSH / PVE / DSM 各自不同），取不到时返回 0，
// 由服务端统一提取的好处是：不用改每一个采集器与 Agent，只要它们往 live.temps 里写温度。
func ExtractTempMax(live json.RawMessage) float64 {
	if len(live) == 0 {
		return 0
	}
	var v struct {
		Temps []struct {
			Value float64 `json:"value"`
		} `json:"temps"`
	}
	if err := json.Unmarshal(live, &v); err != nil {
		return 0
	}
	max := 0.0
	for _, t := range v.Temps {
		if t.Value > max {
			max = t.Value
		}
	}
	return max
}

// AlertRule 告警阈值规则（表 alert_rules；node_id=0 表示全局规则）
type AlertRule struct {
	ID        int64   `json:"id"`
	NodeID    int64   `json:"node_id"`
	Metric    string  `json:"metric"` // cpu | mem_pct | disk_pct
	Op        string  `json:"op"`     // > | >=
	Threshold float64 `json:"threshold"`
	DelaySec  int     `json:"delay_sec"` // 触发延时：连续超阈值 N 秒后才告警；0=立即
	Enabled   bool    `json:"enabled"`
}

// Alert 告警事件（表 alerts）
type Alert struct {
	ID          int64   `json:"id"`
	NodeID      int64   `json:"node_id"`
	NodeName    string  `json:"node_name,omitempty"`
	Metric      string  `json:"metric"` // cpu | mem_pct | disk_pct | offline
	Level       string  `json:"level"`  // warning | critical
	Value       float64 `json:"value"`
	Status      string  `json:"status"` // firing | resolved
	TriggeredAt int64   `json:"triggered_at"`
	ResolvedAt  int64   `json:"resolved_at"`
}

// NodeEvent 节点事件（表 node_events）
type NodeEvent struct {
	ID     int64  `json:"id"`
	NodeID int64  `json:"node_id"`
	TS     int64  `json:"ts"`
	Event  string `json:"event"` // online | offline
}

// SSHCreds 模式 B 连接凭据（序列化后 AES-256-GCM 加密存入 nodes.auth_enc）。
// 模式 C（PVE）复用：Username=API Token 标识（user@realm!tokenid）、Password=Token 密钥、
// NodeName=PVE 节点名（API 路径 /nodes/{node} 需要）。
type SSHCreds struct {
	Username   string `json:"username"`
	Password   string `json:"password,omitempty"`
	PrivateKey string `json:"private_key,omitempty"`
	NodeName   string `json:"node_name,omitempty"`
}

// ParseSSHCreds 解析凭据 JSON。
func ParseSSHCreds(b []byte) (*SSHCreds, error) {
	var c SSHCreds
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	return &c, nil
}
