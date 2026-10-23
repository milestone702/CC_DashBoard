// 告警规则、告警事件与状态查询。
package store

import (
	"database/sql"
	"time"

	"cc_dashboard/server/internal/model"
)

// ListRules 全部阈值规则。
func (db *DB) ListRules() ([]*model.AlertRule, error) {
	rows, err := db.Query(`SELECT id, node_id, metric, op, threshold, delay_sec, enabled FROM alert_rules ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRules(rows)
}

// RulesForNode 指定节点适用的规则（全局 node_id=0 + 节点专属）。
func (db *DB) RulesForNode(nodeID int64) ([]*model.AlertRule, error) {
	rows, err := db.Query(`SELECT id, node_id, metric, op, threshold, delay_sec, enabled FROM alert_rules
		WHERE enabled=1 AND (node_id=0 OR node_id=?)`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRules(rows)
}

func scanRules(rows *sql.Rows) ([]*model.AlertRule, error) {
	var out []*model.AlertRule
	for rows.Next() {
		var r model.AlertRule
		var en int
		if err := rows.Scan(&r.ID, &r.NodeID, &r.Metric, &r.Op, &r.Threshold, &r.DelaySec, &en); err != nil {
			return nil, err
		}
		r.Enabled = en != 0
		out = append(out, &r)
	}
	return out, rows.Err()
}

// CreateRule 新建阈值规则。
func (db *DB) CreateRule(r *model.AlertRule) (int64, error) {
	res, err := db.Exec(`INSERT INTO alert_rules(node_id, metric, op, threshold, delay_sec, enabled) VALUES(?,?,?,?,?,?)`,
		r.NodeID, r.Metric, r.Op, r.Threshold, r.DelaySec, boolInt(r.Enabled))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateRule 更新规则全字段（规则编辑）。
func (db *DB) UpdateRule(r *model.AlertRule) error {
	res, err := db.Exec(`UPDATE alert_rules SET node_id=?, metric=?, op=?, threshold=?, delay_sec=?, enabled=? WHERE id=?`,
		r.NodeID, r.Metric, r.Op, r.Threshold, r.DelaySec, boolInt(r.Enabled), r.ID)
	if err != nil {
		return err
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteRule 删除规则。
func (db *DB) DeleteRule(id int64) error {
	res, err := db.Exec(`DELETE FROM alert_rules WHERE id=?`, id)
	if err != nil {
		return err
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return ErrNotFound
	}
	return nil
}

// FiringAlertNodes 存在“阈值类” firing 告警的节点集合（用于卡片 alert 状态）。
func (db *DB) FiringAlertNodes() (map[int64]bool, error) {
	rows, err := db.Query(`SELECT DISTINCT node_id FROM alerts WHERE status='firing' AND metric!='offline'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// HasFiring 指定节点+指标是否已有 firing 告警。
func (db *DB) HasFiring(nodeID int64, metric string) (bool, error) {
	var n int
	err := db.QueryRow(`SELECT COUNT(1) FROM alerts WHERE node_id=? AND metric=? AND status='firing'`,
		nodeID, metric).Scan(&n)
	return n > 0, err
}

// InsertAlert 记录告警触发。
// 依赖 (node_id, metric) WHERE status='firing' 的唯一索引（见 migrateFiringUnique）：
// 并发上报时同一指标只会产生一条 firing 记录，返回 created=false 表示已存在。
func (db *DB) InsertAlert(nodeID int64, metric, level string, value float64) (bool, error) {
	res, err := db.Exec(`INSERT OR IGNORE INTO alerts(node_id, metric, level, value, status, triggered_at, resolved_at)
		VALUES(?,?,?,?,'firing',?,0)`, nodeID, metric, level, value, time.Now().Unix())
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ResolveAlert 将指定节点+指标的 firing 告警置为 resolved。
// 返回是否真的变更了记录（无 firing 告警时返回 false）——调用方据此决定是否发「恢复」通知。
func (db *DB) ResolveAlert(nodeID int64, metric string) (bool, error) {
	res, err := db.Exec(`UPDATE alerts SET status='resolved', resolved_at=?
		WHERE node_id=? AND metric=? AND status='firing'`, time.Now().Unix(), nodeID, metric)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ListAlerts 查询告警；status 为空返回全部，limit 默认 100。
func (db *DB) ListAlerts(status string, limit int) ([]*model.Alert, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT a.id, a.node_id, COALESCE(n.name,''), a.metric, a.level, a.value,
		a.status, a.triggered_at, a.resolved_at
		FROM alerts a LEFT JOIN nodes n ON n.id=a.node_id`
	args := []any{}
	if status != "" {
		q += ` WHERE a.status=?`
		args = append(args, status)
	}
	q += ` ORDER BY a.id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Alert
	for rows.Next() {
		var a model.Alert
		if err := rows.Scan(&a.ID, &a.NodeID, &a.NodeName, &a.Metric, &a.Level,
			&a.Value, &a.Status, &a.TriggeredAt, &a.ResolvedAt); err != nil {
			return nil, err
		}
		out = append(out, &a)
	}
	return out, rows.Err()
}

// CountByStatus 统计节点在线/离线数量（仅启用节点）。
func (db *DB) CountNodesByStatus() (total, online int, err error) {
	err = db.QueryRow(`SELECT COUNT(1),
		COALESCE(SUM(CASE WHEN status='online' THEN 1 ELSE 0 END),0)
		FROM nodes WHERE enabled=1`).Scan(&total, &online)
	return
}

// CountAlerting 存在 firing 阈值告警的启用节点数。
func (db *DB) CountAlerting() (int, error) {
	var n int
	err := db.QueryRow(`SELECT COUNT(DISTINCT a.node_id) FROM alerts a
		JOIN nodes n ON n.id=a.node_id
		WHERE a.status='firing' AND a.metric!='offline' AND n.enabled=1`).Scan(&n)
	return n, err
}

func errNoRows() error { return sql.ErrNoRows }
