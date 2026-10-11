// 指标写入、实时查询、历史查询与降采样。
package store

import (
	"encoding/json"
	"errors"
	"time"

	"cc_dashboard/server/internal/model"
)

// 历史表名与保留期（秒）。半年/一年视图依赖 metrics_1h 保留 400 天。
const (
	Table5s = "metrics_5s"
	Table1m = "metrics_1m"
	Table1h = "metrics_1h"

	retain5s = 48 * 3600        // 48 小时
	retain1m = 35 * 24 * 3600   // 35 天
	retain1h = 400 * 24 * 3600  // 400 天（覆盖一年视图）
)

const metricCols = `ts, cpu, mem_used, mem_total, disk_pct, net_rx, net_tx, load1, uptime, disk_read, disk_write, per_cpu`

// ResolveRange 将 API 的 range 参数映射为数据表与起始时间。
// 支持：1h / 24h / 7d / 30d / 180d（半年）/ 365d（一年）。
func ResolveRange(rng string, now int64) (table string, from int64, interval int, ok bool) {
	switch rng {
	case "1h":
		return Table5s, now - 3600, 5, true
	case "24h":
		return Table1m, now - 24*3600, 60, true
	case "7d":
		return Table1m, now - 7*24*3600, 60, true
	case "30d":
		return Table1h, now - 30*24*3600, 3600, true
	case "180d":
		return Table1h, now - 180*24*3600, 3600, true
	case "365d":
		return Table1h, now - 365*24*3600, 3600, true
	default:
		return "", 0, 0, false
	}
}

// InsertSample 写入一条实时样本，ts 使用服务端接收时间并回填到样本。
func (db *DB) InsertSample(nodeID int64, s *model.MetricsSample) error {
	ts := time.Now().Unix()
	s.Timestamp = ts
	return db.insertSampleAt(nodeID, ts, s)
}

func (db *DB) insertSampleAt(nodeID, ts int64, s *model.MetricsSample) error {
	perCPU := ""
	if len(s.PerCPU) > 0 {
		if b, err := json.Marshal(s.PerCPU); err == nil {
			perCPU = string(b)
		}
	}
	_, err := db.Exec(`INSERT OR REPLACE INTO metrics_5s
		(node_id, ts, cpu, mem_used, mem_total, disk_pct, net_rx, net_tx, load1, uptime, disk_read, disk_write, per_cpu)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		nodeID, ts, s.CPU, s.MemUsed, s.MemTotal, s.DiskPct, s.NetRx, s.NetTx, s.Load1, s.Uptime,
		s.DiskRead, s.DiskWrite, perCPU)
	return err
}

// LatestSample 单节点最新样本；无数据返回 (nil, nil)。
func (db *DB) LatestSample(nodeID int64) (*model.MetricsSample, error) {
	row := db.QueryRow(`SELECT `+metricCols+` FROM metrics_5s WHERE node_id=? ORDER BY ts DESC LIMIT 1`, nodeID)
	return scanPoint(row)
}

// LatestAll 所有节点的最新样本，key 为 node_id。
func (db *DB) LatestAll() (map[int64]*model.MetricsSample, error) {
	rows, err := db.Query(`SELECT node_id, `+metricCols+` FROM metrics_5s m
		WHERE ts = (SELECT MAX(ts) FROM metrics_5s x WHERE x.node_id = m.node_id)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]*model.MetricsSample{}
	for rows.Next() {
		var id int64
		var s model.MetricsSample
		var perCPU string
		if err := rows.Scan(&id, &s.Timestamp, &s.CPU, &s.MemUsed, &s.MemTotal,
			&s.DiskPct, &s.NetRx, &s.NetTx, &s.Load1, &s.Uptime,
			&s.DiskRead, &s.DiskWrite, &perCPU); err != nil {
			return nil, err
		}
		if perCPU != "" {
			_ = json.Unmarshal([]byte(perCPU), &s.PerCPU)
		}
		out[id] = &s
	}
	return out, rows.Err()
}

// History 查询节点在 [from, to] 闭区间内的历史点。
func (db *DB) History(nodeID int64, table string, from, to int64) ([]*model.HistoryPoint, error) {
	// 表名来自内部常量白名单，不拼接用户输入
	switch table {
	case Table5s, Table1m, Table1h:
	default:
		return nil, errors.New("unknown metrics table")
	}
	q := `SELECT ` + metricCols + ` FROM ` + table + ` WHERE node_id=? AND ts>=? AND ts<=? ORDER BY ts`
	rows, err := db.Query(q, nodeID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.HistoryPoint
	for rows.Next() {
		var p model.HistoryPoint
		if err := rows.Scan(&p.TS, &p.CPU, &p.MemUsed, &p.MemTotal,
			&p.DiskPct, &p.NetRx, &p.NetTx, &p.Load1, &p.Uptime,
			&p.DiskRead, &p.DiskWrite, &p.PerCPU); err != nil {
			return nil, err
		}
		out = append(out, &p)
	}
	return out, rows.Err()
}

// RollupFresh 查询前补齐最近聚合窗口（含当前未完成窗口）。
// 未完成窗口先写入粗结果，随后台任务在窗口结束时用 INSERT OR REPLACE 覆盖修正，
// 避免刚上报的数据在 24h/7d 视图中出现空窗。
func (db *DB) RollupFresh(now int64) {
	_ = db.AggregateUp(Table5s, Table1m, 60, now-1800, now-mod(now, 60)+60)
	_ = db.AggregateUp(Table1m, Table1h, 3600, now-3*3600, now-mod(now, 3600)+3600)
}

// AggregateUp 将 src 表在每个 period 窗口内的数据聚合写入 dst 表。
// 仅聚合已完整结束的窗口（t+period <= to）。幂等：INSERT OR REPLACE。
// 聚合口径：数值列 AVG（uptime 取 MAX）；per_cpu 取窗口内首个非空样本
// （JSON 数组无法 SQL 平均，逐核曲线用单点近似，误差 ≤ 窗口内波动）。
func (db *DB) AggregateUp(src, dst string, period, from, to int64) error {
	if src != Table5s && src != Table1m || dst != Table1m && dst != Table1h {
		return errors.New("invalid aggregate tables")
	}
	stmt, err := db.Prepare(`INSERT OR REPLACE INTO ` + dst + `
		(node_id, ts, cpu, mem_used, mem_total, disk_pct, net_rx, net_tx, load1, uptime, disk_read, disk_write, per_cpu)
		SELECT node_id, ?, AVG(cpu),
		       CAST(AVG(mem_used) AS INTEGER), CAST(AVG(mem_total) AS INTEGER),
		       AVG(disk_pct),
		       CAST(AVG(net_rx) AS INTEGER), CAST(AVG(net_tx) AS INTEGER),
		       AVG(load1), CAST(MAX(uptime) AS INTEGER),
		       CAST(AVG(disk_read) AS INTEGER), CAST(AVG(disk_write) AS INTEGER),
		       COALESCE((
		         SELECT s2.per_cpu FROM ` + src + ` s2
		         WHERE s2.node_id = m.node_id AND s2.ts >= ? AND s2.ts < ? AND s2.per_cpu != ''
		         ORDER BY s2.ts LIMIT 1
		       ), '')
		FROM ` + src + ` m WHERE ts>=? AND ts<? GROUP BY node_id`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for t := from - mod(from, period); t+period <= to; t += period {
		if _, err := stmt.Exec(t, t, t+period, t, t+period); err != nil {
			return err
		}
	}
	return nil
}

// Cleanup 清理超过保留期的历史数据。
func (db *DB) Cleanup(now int64) error {
	for _, c := range []struct {
		table   string
		keepSec int64
	}{{Table5s, retain5s}, {Table1m, retain1m}, {Table1h, retain1h}} {
		if _, err := db.Exec(`DELETE FROM `+c.table+` WHERE ts < ?`, now-c.keepSec); err != nil {
			return err
		}
	}
	return nil
}

func mod(a, b int64) int64 {
	m := a % b
	if m < 0 {
		m += b
	}
	return m
}

func scanPoint(row interface{ Scan(...any) error }) (*model.MetricsSample, error) {
	var s model.MetricsSample
	var perCPU string
	err := row.Scan(&s.Timestamp, &s.CPU, &s.MemUsed, &s.MemTotal,
		&s.DiskPct, &s.NetRx, &s.NetTx, &s.Load1, &s.Uptime,
		&s.DiskRead, &s.DiskWrite, &perCPU)
	if errors.Is(err, errNoRows()) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if perCPU != "" {
		_ = json.Unmarshal([]byte(perCPU), &s.PerCPU)
	}
	return &s, nil
}
