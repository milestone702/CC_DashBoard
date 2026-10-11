// 模式 B：SSH 远程轮询采集引擎。
// 目标机无需安装任何 Agent：Master 通过 SSH 执行内联采集命令并解析，
// 复用与 /report 相同的入库 / 告警 / WebSocket 链路。
package collector

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"cc_dashboard/server/internal/alert"
	"cc_dashboard/server/internal/model"
	"cc_dashboard/server/internal/security"
	"cc_dashboard/server/internal/store"
)

// Manager SSH 轮询引擎。
type Manager struct {
	db        *store.DB
	key       []byte
	engine    *alert.Engine
	broadcast func(any) // 推送 metrics 消息（由 main 注入 hub.Broadcast）

	mu      sync.Mutex
	prevCPU map[int64]cpuSample // 节点 → 上次 CPU 时间片（差分计算使用率）
	prevPerCores map[int64][]cpuSample // 节点 → 每核时间片（/proc/stat 差分）
	prevNet map[int64]netSample // 节点 → 上次网卡字节计数
	prevPVE map[int64]pveNetSample   // PVE netin/netout 差分
	prevESXI map[int64]esxiNetSample   // ESXi 网卡合计累计差分
	prevESXINic  map[int64]map[string]esxiNetSample // ESXi 单网卡差分
	prevESXINicTS map[int64]time.Time
	prevESXIDisk map[int64]map[string]esxiDiskSample // ESXi 磁盘累计差分
	sids    map[int64]synoSID        // 群晖 SID 会话缓存
	synoNames map[int64]string       // 群晖主机名缓存（server_name 极少变化）
	clients map[int64]*ssh.Client    // SSH 长连接缓存（复用）
	pubIPTs map[int64]time.Time       // SSH 节点公网 IP 上次查询时间（10 分钟节流）

	// OnLive 回调：缓存实时明细到 API 层（详情接口 REST 兜底读取）
	OnLive func(nodeID int64, data json.RawMessage)
}

type cpuSample struct {
	total, idle float64
	ts          time.Time
}

type netSample struct {
	rx, tx uint64
	ts     time.Time
}

// NewManager 创建 SSH 采集引擎。broadcast 为 nil 时不推送。
func NewManager(db *store.DB, key []byte, engine *alert.Engine, broadcast func(any)) *Manager {
	return &Manager{
		db: db, key: key, engine: engine, broadcast: broadcast,
		prevCPU:  map[int64]cpuSample{},
		prevPerCores: map[int64][]cpuSample{},
		prevNet:  map[int64]netSample{},
		prevPVE:  map[int64]pveNetSample{},
		prevESXI: map[int64]esxiNetSample{},
		prevESXINic:  map[int64]map[string]esxiNetSample{},
		prevESXINicTS: map[int64]time.Time{},
		prevESXIDisk: map[int64]map[string]esxiDiskSample{},
		sids:     map[int64]synoSID{},
		synoNames: map[int64]string{},
		clients:  map[int64]*ssh.Client{},
		pubIPTs:  map[int64]time.Time{},
	}
}

// Start 启动轮询循环（每 5 秒扫描一次，按节点上报周期节流）。
// 同时驱动模式 B（SSH 轮询）与正向连接（pull 拉取）。
func (m *Manager) Start() {
	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for range t.C {
			now := time.Now().Unix()
			m.scan()
			m.scanPull(now)
			m.scanAPI(now)
		}
	}()
	log.Printf("采集引擎已启动（SSH / 正向拉取 / PVE API）")
}

func (m *Manager) scan() {
	nodes, err := m.db.ListNodes()
	if err != nil {
		return
	}
	now := time.Now().Unix()
	alive := map[int64]bool{}
	for _, n := range nodes {
		if n.AccessMode != model.ModeSSH || !n.Enabled || !n.Approved {
			continue
		}
		alive[n.ID] = true
		interval := n.ReportInterval
		if interval <= 0 {
			interval = 15 // SSH 模式默认 15s，降低连接开销
		}
		if n.LastSeen > 0 && now-n.LastSeen < int64(interval)-1 {
			continue // 未到下个采集周期
		}
		m.poll(n, interval)
	}
	m.reapClients(alive)
}

// poll 单节点采集：SSH 连接 → 执行命令 → 解析 → 入库/告警/推送。
func (m *Manager) poll(n *model.Node, interval int) {
	creds, err := m.decryptCreds(n)
	if err != nil {
		log.Printf("SSH[%s] 凭据解密失败: %v", n.Name, err)
		return
	}
	// 长连接：命中缓存先探活，失效自动重拨；采完不关闭
	client, err := m.getSSHClient(n, creds)
	if err != nil {
		log.Printf("SSH[%s] 连接失败 %s:%d: %v", n.Name, n.Address, sshPort(n), err)
		return // 保持 LastSeen 不变 → 超时后由离线巡检判定
	}
	// 目标机公网 IP（best-effort，异步不阻塞采集）：SSH 模式无 Agent 上报，由服务端在目标机上查询
	go m.maybeFetchPublicIP(n, client)

	var (
		sample *model.MetricsSample
		detail map[string]any
		live   map[string]any
	)
	switch {
	case n.OSType == model.OSTypeESXi:
		sample, detail, live, err = m.runESXI(client, n)
	case n.OSType == model.OSTypeWindows:
		sample, detail, live, err = m.runWindows(client, n)
	default:
		sample, detail, live, err = m.runLinux(client, n)
	}
	if err != nil {
		log.Printf("SSH[%s] 采集失败: %v", n.Name, err)
		return
	}
	sample.NodeID = n.ID

	detailStr := ""
	if detail != nil {
		detailStr, _ = marshalJSON(detail)
	}
	var liveRaw json.RawMessage
	if live != nil {
		liveRaw, _ = json.Marshal(live)
	}
	m.ingest(n, sample, detailStr, "", liveRaw)
}

// ingest 统一入库链路：样本 → 详情/硬件 → 实时 → 状态/告警 → WS 推送。
// SSH / 拉取 / PVE 三种采集共用。
func (m *Manager) ingest(n *model.Node, sample *model.MetricsSample, detailJSON, hardwareJSON string, live json.RawMessage) {
	if err := m.db.InsertSample(n.ID, sample); err != nil {
		log.Printf("[%s] 入库失败: %v", n.Name, err)
		return
	}
	if detailJSON != "" && detailJSON != "null" {
		_ = m.db.SaveNodeDetail(n.ID, detailJSON)
	}
	if hardwareJSON != "" && hardwareJSON != "null" {
		_ = m.db.SaveNodeHardware(n.ID, hardwareJSON)
	}
	if m.broadcast != nil && len(live) > 0 && string(live) != "null" {
		if m.OnLive != nil {
			m.OnLive(n.ID, live)
		}
		m.broadcast(map[string]any{"type": "live", "node_id": n.ID, "data": live})
	}
	if changed, err := m.db.SetNodeOnline(n.ID); err == nil && changed {
		m.engine.SetOnline(n.ID)
	}
	m.engine.EvaluateSample(n.ID, sample)
	if m.broadcast != nil {
		m.broadcast(map[string]any{"type": "metrics", "node_id": n.ID, "data": sample})
	}
}

func (m *Manager) dial(n *model.Node, creds *model.SSHCreds) (*ssh.Client, error) {
	var auths []ssh.AuthMethod
	if creds.Password != "" {
		auths = append(auths, ssh.Password(creds.Password))
		// ESXi / 部分 Linux 仅开放 keyboard-interactive（禁用 password 方法）→ 用同一密码应答
		auths = append(auths, ssh.KeyboardInteractive(func(_, _ string, questions []string, _ []bool) ([]string, error) {
			answers := make([]string, len(questions))
			for i := range questions {
				answers[i] = creds.Password
			}
			return answers, nil
		}))
	}
	if creds.PrivateKey != "" {
		signer, err := ssh.ParsePrivateKey([]byte(creds.PrivateKey))
		if err != nil {
			return nil, fmt.Errorf("解析私钥失败: %w", err)
		}
		auths = append(auths, ssh.PublicKeys(signer))
	}
	if len(auths) == 0 {
		return nil, fmt.Errorf("未配置用户名密码或私钥")
	}
	user := creds.Username
	if user == "" {
		user = "root"
	}
	cfg := &ssh.ClientConfig{
		User: user,
		Auth: auths,
		// TOFU 主机密钥校验：首次连接记录指纹，之后不匹配即拒绝（可用 rotate_host_key 重置）
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			fp := ssh.FingerprintSHA256(key)
			stored, err := m.db.GetNodeHostKey(n.ID)
			if err != nil {
				return err
			}
			if stored == "" {
				if err := m.db.SaveNodeHostKey(n.ID, fp); err != nil {
					return err
				}
				log.Printf("SSH[%s] 已记录主机指纹 %s（TOFU 首次信任）", n.Name, fp)
				return nil
			}
			if stored != fp {
				return fmt.Errorf("主机密钥变化: 期望 %s 实际 %s（确认无误后 PUT /nodes/:id {\"rotate_host_key\":true} 重置）", stored, fp)
			}
			return nil
		},
		Timeout: 10 * time.Second,
	}
	addr := fmt.Sprintf("%s:%d", n.Address, sshPort(n))
	return ssh.Dial("tcp", addr, cfg)
}

// getSSHClient 返回可复用的长连接：缓存连接先开会话探活，失败则关闭并重拨。
func (m *Manager) getSSHClient(n *model.Node, creds *model.SSHCreds) (*ssh.Client, error) {
	m.mu.Lock()
	cached := m.clients[n.ID]
	m.mu.Unlock()
	if cached != nil {
		if sess, err := cached.NewSession(); err == nil {
			sess.Close()
			return cached, nil
		}
		m.dropClient(n.ID, cached)
	}
	c, err := m.dial(n, creds)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.clients[n.ID] = c
	m.mu.Unlock()
	return c, nil
}

// dropClient 关闭并移除指定连接（仅当仍是当前缓存时删除）。
func (m *Manager) dropClient(id int64, c *ssh.Client) {
	m.mu.Lock()
	if cur, ok := m.clients[id]; ok && cur == c {
		delete(m.clients, id)
	}
	m.mu.Unlock()
	_ = c.Close()
}

// reapClients 关闭已不存在（被删除/停用）节点的连接。
func (m *Manager) reapClients(alive map[int64]bool) {
	m.mu.Lock()
	var stale []*ssh.Client
	for id, c := range m.clients {
		if !alive[id] {
			stale = append(stale, c)
			delete(m.clients, id)
		}
	}
	m.mu.Unlock()
	for _, c := range stale {
		_ = c.Close()
	}
}

func sshPort(n *model.Node) int {
	if n.Port > 0 {
		return n.Port
	}
	return 22
}

func (m *Manager) decryptCreds(n *model.Node) (*model.SSHCreds, error) {
	if n.AuthEnc == "" {
		return &model.SSHCreds{}, nil
	}
	plain, err := security.Decrypt(m.key, []byte(n.AuthEnc))
	if err != nil {
		return nil, err
	}
	return model.ParseSSHCreds(plain)
}

// ---------- SSH 节点公网 IP（模式 B 无 Agent 上报，由服务端在目标机上执行查询命令） ----------

// maybeFetchPublicIP 每 10 分钟在目标机上查询一次公网 IP，成功则写入节点 public_ip。
// 查询失败（目标机无外网 / 命令不存在）静默，等下个周期重试；手动 PUT 的值会被后续成功查询覆盖。
func (m *Manager) maybeFetchPublicIP(n *model.Node, client *ssh.Client) {
	m.mu.Lock()
	last := m.pubIPTs[n.ID]
	m.pubIPTs[n.ID] = time.Now() // 先打点：失败也等下个周期，避免每轮风暴
	m.mu.Unlock()
	if !last.IsZero() && time.Since(last) < 10*time.Minute {
		return
	}
	cmd := `wget -qT5 -O- https://api.ipify.org 2>/dev/null || curl -sm5 https://api.ipify.org 2>/dev/null`
	if n.OSType == model.OSTypeWindows {
		cmd = `powershell -NoProfile -Command Invoke-RestMethod -Uri https://api.ipify.org -TimeoutSec 5`
	}
	out, err := runSSH(client, cmd, 8*time.Second)
	if err != nil {
		return
	}
	ip := strings.TrimSpace(out)
	if parsed := net.ParseIP(ip); parsed == nil || !parsed.IsGlobalUnicast() || parsed.IsPrivate() || parsed.IsLoopback() {
		return
	}
	if n.PublicIP == ip {
		return
	}
	_ = m.db.SetNodePublicIP(n.ID, ip)
}

// runSSH 在长连接上执行命令（新会话），超时中断。
func runSSH(client *ssh.Client, cmd string, timeout time.Duration) (string, error) {
	sess, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer sess.Close()
	type result struct {
		out []byte
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, err := sess.Output(cmd)
		done <- result{out, err}
	}()
	select {
	case r := <-done:
		return string(r.out), r.err
	case <-time.After(timeout):
		_ = sess.Close() // 关闭会话以中断仍在执行的远程命令
		return "", fmt.Errorf("远程命令超时 %s", timeout)
	}
}
