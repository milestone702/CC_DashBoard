// 节点 CRUD 与状态维护。
package store

import (
	"database/sql"
	"errors"
	"time"

	"cc_dashboard/server/internal/model"
)

// ErrNotFound 目标不存在
var ErrNotFound = errors.New("not found")

const nodeCols = `id, name, os_type, access_mode, address, port, token, enabled, approved, direction,
	report_interval, status, last_seen, created_at, auth_enc, sort_order,
	public_ip, agent_version, agent_goos, agent_arch, last_error, last_error_at, group_tag, mute_until, agent_mem`

func scanNode(row interface{ Scan(...any) error }) (*model.Node, error) {
	var n model.Node
	var enabled, approved int
	err := row.Scan(&n.ID, &n.Name, &n.OSType, &n.AccessMode, &n.Address, &n.Port,
		&n.Token, &enabled, &approved, &n.Direction, &n.ReportInterval, &n.Status, &n.LastSeen, &n.CreatedAt, &n.AuthEnc, &n.SortOrder,
		&n.PublicIP, &n.AgentVersion, &n.AgentGoos, &n.AgentArch, &n.LastError, &n.LastErrorAt, &n.GroupTag, &n.MuteUntil, &n.AgentMem)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	n.Enabled = enabled != 0
	n.Approved = approved != 0
	if n.Direction == "" {
		n.Direction = "push"
	}
	return &n, nil
}

// ListNodes 全部节点（按手动排序 sort_order，缺省按创建顺序）。
func (db *DB) ListNodes() ([]*model.Node, error) {
	rows, err := db.Query(`SELECT ` + nodeCols + ` FROM nodes ORDER BY sort_order, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// GetNode 按 ID 查询。
func (db *DB) GetNode(id int64) (*model.Node, error) {
	return scanNode(db.QueryRow(`SELECT `+nodeCols+` FROM nodes WHERE id=?`, id))
}

// FindNodeByToken 按上报 Token 查询（Token 为空返回 not found）。
func (db *DB) FindNodeByToken(token string) (*model.Node, error) {
	if token == "" {
		return nil, ErrNotFound
	}
	return scanNode(db.QueryRow(`SELECT `+nodeCols+` FROM nodes WHERE token=?`, token))
}

// CreateNode 创建节点，返回自增 ID。
func (db *DB) CreateNode(n *model.Node) (int64, error) {
	if n.ReportInterval <= 0 {
		n.ReportInterval = 5
	}
	if n.Status == "" {
		n.Status = model.StatusOffline
	}
	if n.CreatedAt == 0 {
		n.CreatedAt = time.Now().Unix()
	}
	res, err := db.Exec(`INSERT INTO nodes
		(name, os_type, access_mode, address, port, token, enabled, approved, direction, report_interval, status, last_seen, created_at, auth_enc, sort_order, group_tag)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?, (SELECT COALESCE(MAX(sort_order),0)+1 FROM nodes), ?)`,
		n.Name, n.OSType, n.AccessMode, n.Address, n.Port, n.Token,
		boolInt(n.Enabled), boolInt(n.Approved), orPush(n.Direction), n.ReportInterval, n.Status, n.LastSeen, n.CreatedAt, n.AuthEnc, n.GroupTag)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ReorderNodes 按传入 id 顺序批量写入 sort_order（管理端排序；未包含的节点保持原值）。
func (db *DB) ReorderNodes(ids []int64) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, id := range ids {
		if _, err := tx.Exec(`UPDATE nodes SET sort_order=? WHERE id=?`, (i+1)*10, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UpdateNode 按非 nil 字段部分更新（传入的 n.ID 必填）。
func (db *DB) UpdateNode(n *model.Node) error {
	res, err := db.Exec(`UPDATE nodes SET name=?, os_type=?, access_mode=?, address=?, port=?,
		enabled=?, approved=?, direction=?, report_interval=?, token=?, auth_enc=?, group_tag=?, mute_until=? WHERE id=?`,
		n.Name, n.OSType, n.AccessMode, n.Address, n.Port,
		boolInt(n.Enabled), boolInt(n.Approved), orPush(n.Direction), n.ReportInterval, n.Token, n.AuthEnc, n.GroupTag, n.MuteUntil, n.ID)
	if err != nil {
		return err
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteNode 删除节点及其全部历史与详情。
func (db *DB) DeleteNode(id int64) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM nodes WHERE id=?`,
		`DELETE FROM node_details WHERE node_id=?`,
		`DELETE FROM node_hardware WHERE node_id=?`,
		`DELETE FROM metrics_5s WHERE node_id=?`,
		`DELETE FROM metrics_1m WHERE node_id=?`,
		`DELETE FROM metrics_1h WHERE node_id=?`,
		`DELETE FROM alerts WHERE node_id=?`,
		`DELETE FROM node_events WHERE node_id=?`,
		`DELETE FROM boot_events WHERE node_id=?`,
		`DELETE FROM alert_rules WHERE node_id=?`,
	} {
		if _, err := tx.Exec(q, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SetNodeOnline 标记在线并刷新 last_seen；状态发生变更时写入事件，返回是否刚上线。
func (db *DB) SetNodeOnline(id int64) (changed bool, err error) {
	now := time.Now().Unix()
	tx, err := db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var status string
	if err := tx.QueryRow(`SELECT status FROM nodes WHERE id=?`, id).Scan(&status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, ErrNotFound
		}
		return false, err
	}
	if _, err := tx.Exec(`UPDATE nodes SET status=?, last_seen=? WHERE id=?`, model.StatusOnline, now, id); err != nil {
		return false, err
	}
	if status != model.StatusOnline {
		if _, err := tx.Exec(`INSERT INTO node_events(node_id, ts, event) VALUES(?,?,?)`,
			id, now, model.StatusOnline); err != nil {
			return false, err
		}
		changed = true
	}
	return changed, tx.Commit()
}

// MarkNodeOffline 标记离线并写入事件；状态已是离线则不重复写。
func (db *DB) MarkNodeOffline(id int64) error {
	now := time.Now().Unix()
	res, err := db.Exec(`UPDATE nodes SET status=? WHERE id=? AND status!=?`,
		model.StatusOffline, id, model.StatusOffline)
	if err != nil {
		return err
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return nil
	}
	_, err = db.Exec(`INSERT INTO node_events(node_id, ts, event) VALUES(?,?,?)`,
		id, now, model.StatusOffline)
	return err
}

// OfflineCandidates 返回可能已离线（超过 3×上报周期未上报，最小 15 秒）的在线节点。
// 阈值下推到 SQL，避免每 10 秒把全部在线节点捞出来在内存里过滤。
func (db *DB) OfflineCandidates(now int64) ([]*model.Node, error) {
	rows, err := db.Query(`SELECT `+nodeCols+` FROM nodes
		WHERE enabled=1 AND status=? AND last_seen>0
		  AND last_seen < ? - MAX(15, report_interval*3)`,
		model.StatusOnline, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// SaveNodeDetail 保存 Agent 上报的系统详情 JSON（原样存储）。
func (db *DB) SaveNodeDetail(nodeID int64, data string) error {
	_, err := db.Exec(`INSERT INTO node_details(node_id, data, updated_at) VALUES(?,?,?)
		ON CONFLICT(node_id) DO UPDATE SET data=excluded.data, updated_at=excluded.updated_at`,
		nodeID, data, time.Now().Unix())
	return err
}

// GetNodeDetail 读取系统详情 JSON；无记录返回空串。
func (db *DB) GetNodeDetail(nodeID int64) (string, error) {
	var data string
	err := db.QueryRow(`SELECT data FROM node_details WHERE node_id=?`, nodeID).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return data, err
}

// SaveNodeHardware 保存硬件清单 JSON（Agent 上报，低频变更）。
func (db *DB) SaveNodeHardware(nodeID int64, data string) error {
	_, err := db.Exec(`INSERT INTO node_hardware(node_id, data, updated_at) VALUES(?,?,?)
		ON CONFLICT(node_id) DO UPDATE SET data=excluded.data, updated_at=excluded.updated_at`,
		nodeID, data, time.Now().Unix())
	return err
}

// GetNodeHardware 读取硬件清单 JSON；无记录返回空串。
func (db *DB) GetNodeHardware(nodeID int64) (string, error) {
	var data string
	err := db.QueryRow(`SELECT data FROM node_hardware WHERE node_id=?`, nodeID).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return data, err
}

// RecentEvents 最近 N 条节点事件（新→旧）。
func (db *DB) RecentEvents(nodeID int64, limit int) ([]*model.NodeEvent, error) {
	rows, err := db.Query(`SELECT id, node_id, ts, event FROM node_events
		WHERE node_id=? ORDER BY id DESC LIMIT ?`, nodeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.NodeEvent
	for rows.Next() {
		var e model.NodeEvent
		if err := rows.Scan(&e.ID, &e.NodeID, &e.TS, &e.Event); err != nil {
			return nil, err
		}
		out = append(out, &e)
	}
	return out, rows.Err()
}

// GetNodeHostKey 读取 TOFU 已知主机指纹；无记录返回空串。
func (db *DB) GetNodeHostKey(id int64) (string, error) {
	var k string
	err := db.QueryRow(`SELECT host_key FROM nodes WHERE id=?`, id).Scan(&k)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return k, err
}

// SaveNodeHostKey 首次连接时写入指纹（仅当尚未记录）。
func (db *DB) SaveNodeHostKey(id int64, fp string) error {
	_, err := db.Exec(`UPDATE nodes SET host_key=? WHERE id=? AND (host_key='' OR host_key IS NULL)`, fp, id)
	return err
}

// ClearNodeHostKey 重置主机指纹（密钥变更后重新信任）。
func (db *DB) ClearNodeHostKey(id int64) error {
	_, err := db.Exec(`UPDATE nodes SET host_key='' WHERE id=?`, id)
	return err
}

// UpdateNodeAgentMeta 刷新 Agent 上报的身份元数据（版本/目标平台）。
func (db *DB) UpdateNodeAgentMeta(id int64, version, goos, arch string) error {
	_, err := db.Exec(`UPDATE nodes SET agent_version=?, agent_goos=?, agent_arch=? WHERE id=?`,
		version, goos, arch, id)
	return err
}

// SetNodePublicIP 写入公网 IP（服务端解析的源 IP 或 Agent 上报值）。
func (db *DB) SetNodePublicIP(id int64, ip string) error {
	_, err := db.Exec(`UPDATE nodes SET public_ip=? WHERE id=?`, ip, id)
	return err
}

// SetNodeError 记录最近一次采集/连接失败原因，供面板解释「为什么离线/没数据」。
// 采集成功后由 ClearNodeError 清空。
func (db *DB) SetNodeError(id int64, msg string) error {
	if r := []rune(msg); len(r) > 200 { // 按字符截断，避免多字节被劈开，也防止异常输出撑爆一行
		msg = string(r[:200])
	}
	_, err := db.Exec(`UPDATE nodes SET last_error=?, last_error_at=? WHERE id=?`,
		msg, time.Now().Unix(), id)
	return err
}

// ClearNodeError 采集成功后清除错误标记（仅在有值时写，减少写放大）。
func (db *DB) ClearNodeError(id int64) error {
	_, err := db.Exec(`UPDATE nodes SET last_error='', last_error_at=0 WHERE id=? AND last_error!=''`, id)
	return err
}

// SetNodeAgentMem 记录 Agent 自身内存占用（字节）。
func (db *DB) SetNodeAgentMem(id int64, mem int64) error {
	_, err := db.Exec(`UPDATE nodes SET agent_mem=? WHERE id=?`, mem, id)
	return err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func orPush(d string) string {
	if d == "" {
		return "push"
	}
	return d
}
