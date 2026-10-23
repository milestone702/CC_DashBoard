// 建表迁移。表结构依据《CC_DashBoard_开发文档》7.2。
package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

// nodesTableDef 是 nodes 表的列定义。建表、「补列」与「数值列类型修正」
// 重建表共用这一份，避免多处定义漂移。
const nodesTableDef = `(
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  name            TEXT    NOT NULL,
  os_type         TEXT    NOT NULL DEFAULT 'linux',
  access_mode     TEXT    NOT NULL DEFAULT 'agent',
  address         TEXT    NOT NULL DEFAULT '',
  port            INTEGER NOT NULL DEFAULT 0,
  token           TEXT    NOT NULL DEFAULT '',
  enabled         INTEGER NOT NULL DEFAULT 1,
  approved        INTEGER NOT NULL DEFAULT 1,
  direction       TEXT    NOT NULL DEFAULT 'push',
  report_interval INTEGER NOT NULL DEFAULT 5,
  status          TEXT    NOT NULL DEFAULT 'offline',
  last_seen       INTEGER NOT NULL DEFAULT 0,
  created_at      INTEGER NOT NULL DEFAULT 0,
  sort_order      INTEGER NOT NULL DEFAULT 0,
  auth_enc        TEXT    NOT NULL DEFAULT '',
  host_key        TEXT    NOT NULL DEFAULT '',
  public_ip       TEXT    NOT NULL DEFAULT '',
  agent_version   TEXT    NOT NULL DEFAULT '',
  agent_goos      TEXT    NOT NULL DEFAULT '',
  agent_arch      TEXT    NOT NULL DEFAULT '',
  last_error      TEXT    NOT NULL DEFAULT '',
  last_error_at   INTEGER NOT NULL DEFAULT 0,
  group_tag       TEXT    NOT NULL DEFAULT '',
  mute_until      INTEGER NOT NULL DEFAULT 0,
  agent_mem       INTEGER NOT NULL DEFAULT 0
)`

const schema = `
CREATE TABLE IF NOT EXISTS nodes ` + nodesTableDef + `;
CREATE UNIQUE INDEX IF NOT EXISTS idx_nodes_token ON nodes(token);

CREATE TABLE IF NOT EXISTS node_details (
  node_id    INTEGER PRIMARY KEY,
  data       TEXT    NOT NULL,
  updated_at INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS node_hardware (
  node_id    INTEGER PRIMARY KEY,
  data       TEXT    NOT NULL,
  updated_at INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS metrics_5s (
  node_id    INTEGER NOT NULL,
  ts         INTEGER NOT NULL,
  cpu        REAL    NOT NULL DEFAULT 0,
  mem_used   INTEGER NOT NULL DEFAULT 0,
  mem_total  INTEGER NOT NULL DEFAULT 0,
  disk_pct   REAL    NOT NULL DEFAULT 0,
  net_rx     INTEGER NOT NULL DEFAULT 0,
  net_tx     INTEGER NOT NULL DEFAULT 0,
  load1      REAL    NOT NULL DEFAULT 0,
  uptime     INTEGER NOT NULL DEFAULT 0,
  disk_read  INTEGER NOT NULL DEFAULT 0,
  disk_write INTEGER NOT NULL DEFAULT 0,
  per_cpu    TEXT    NOT NULL DEFAULT '',
  temp_max   REAL    NOT NULL DEFAULT 0,
  PRIMARY KEY (node_id, ts)
);

CREATE TABLE IF NOT EXISTS metrics_1m (
  node_id    INTEGER NOT NULL,
  ts         INTEGER NOT NULL,
  cpu        REAL    NOT NULL DEFAULT 0,
  mem_used   INTEGER NOT NULL DEFAULT 0,
  mem_total  INTEGER NOT NULL DEFAULT 0,
  disk_pct   REAL    NOT NULL DEFAULT 0,
  net_rx     INTEGER NOT NULL DEFAULT 0,
  net_tx     INTEGER NOT NULL DEFAULT 0,
  load1      REAL    NOT NULL DEFAULT 0,
  uptime     INTEGER NOT NULL DEFAULT 0,
  disk_read  INTEGER NOT NULL DEFAULT 0,
  disk_write INTEGER NOT NULL DEFAULT 0,
  per_cpu    TEXT    NOT NULL DEFAULT '',
  temp_max   REAL    NOT NULL DEFAULT 0,
  PRIMARY KEY (node_id, ts)
);

CREATE TABLE IF NOT EXISTS metrics_1h (
  node_id    INTEGER NOT NULL,
  ts         INTEGER NOT NULL,
  cpu        REAL    NOT NULL DEFAULT 0,
  mem_used   INTEGER NOT NULL DEFAULT 0,
  mem_total  INTEGER NOT NULL DEFAULT 0,
  disk_pct   REAL    NOT NULL DEFAULT 0,
  net_rx     INTEGER NOT NULL DEFAULT 0,
  net_tx     INTEGER NOT NULL DEFAULT 0,
  load1      REAL    NOT NULL DEFAULT 0,
  uptime     INTEGER NOT NULL DEFAULT 0,
  disk_read  INTEGER NOT NULL DEFAULT 0,
  disk_write INTEGER NOT NULL DEFAULT 0,
  per_cpu    TEXT    NOT NULL DEFAULT '',
  temp_max   REAL    NOT NULL DEFAULT 0,
  PRIMARY KEY (node_id, ts)
);
CREATE INDEX IF NOT EXISTS idx_5s_ts  ON metrics_5s(ts);
CREATE INDEX IF NOT EXISTS idx_1m_ts  ON metrics_1m(ts);
CREATE INDEX IF NOT EXISTS idx_1h_ts  ON metrics_1h(ts);

CREATE TABLE IF NOT EXISTS alert_rules (
  id        INTEGER PRIMARY KEY AUTOINCREMENT,
  node_id   INTEGER NOT NULL DEFAULT 0,
  metric    TEXT    NOT NULL,
  op        TEXT    NOT NULL DEFAULT '>',
  threshold REAL    NOT NULL,
  delay_sec INTEGER NOT NULL DEFAULT 0,
  enabled   INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE IF NOT EXISTS alerts (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  node_id      INTEGER NOT NULL,
  metric       TEXT    NOT NULL,
  level        TEXT    NOT NULL DEFAULT 'warning',
  value        REAL    NOT NULL DEFAULT 0,
  status       TEXT    NOT NULL DEFAULT 'firing',
  triggered_at INTEGER NOT NULL DEFAULT 0,
  resolved_at  INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_alerts_status ON alerts(status, node_id, metric);

CREATE TABLE IF NOT EXISTS node_events (
  id      INTEGER PRIMARY KEY AUTOINCREMENT,
  node_id INTEGER NOT NULL,
  ts      INTEGER NOT NULL,
  event   TEXT    NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_events_node ON node_events(node_id, ts);

CREATE TABLE IF NOT EXISTS settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

-- 通知发送记录（成功/失败均落库，重试耗尽后仍保留最后错误）
CREATE TABLE IF NOT EXISTS notify_logs (
  id      INTEGER PRIMARY KEY AUTOINCREMENT,
  ts      INTEGER NOT NULL,
  channel TEXT    NOT NULL,
  title   TEXT    NOT NULL,
  ok      INTEGER NOT NULL DEFAULT 0,
  error   TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_notify_logs_ts ON notify_logs(ts);

-- 程序池（Agent/Dashboard 安装包元数据，文件存 data/pool/）
CREATE TABLE IF NOT EXISTS pool_items (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  component  TEXT    NOT NULL,             -- agent | server
  version    TEXT    NOT NULL,
  goos       TEXT    NOT NULL DEFAULT '',
  arch       TEXT    NOT NULL DEFAULT '',
  format     TEXT    NOT NULL DEFAULT 'bin', -- bin | deb | rpm | ipk
  filename   TEXT    NOT NULL,             -- data/pool/ 下的实际文件名
  sha256     TEXT    NOT NULL,
  size       INTEGER NOT NULL DEFAULT 0,
  note       TEXT    NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_pool_latest ON pool_items(component, goos, arch, created_at);

-- 开关机记录（(node_id, ts, type) 主键即插入去重）
CREATE TABLE IF NOT EXISTS boot_events (
  node_id INTEGER NOT NULL,
  ts      INTEGER NOT NULL,
  type    TEXT    NOT NULL,               -- on | off
  PRIMARY KEY (node_id, ts, type)
);

-- 机器凭证（多 Key 支持；只存 SHA-256）
CREATE TABLE IF NOT EXISTS api_keys (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  name       TEXT    NOT NULL DEFAULT '',
  hash       TEXT    NOT NULL UNIQUE,
  scope      TEXT    NOT NULL DEFAULT 'view',
  created_at INTEGER NOT NULL DEFAULT 0
);

-- 操作审计（管理员写操作；保留 90 天）
CREATE TABLE IF NOT EXISTS audit_logs (
  id     INTEGER PRIMARY KEY AUTOINCREMENT,
  ts     INTEGER NOT NULL,
  actor  TEXT    NOT NULL DEFAULT '',
  method TEXT    NOT NULL,
  path   TEXT    NOT NULL,
  status INTEGER NOT NULL DEFAULT 0,
  ip     TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_audit_ts ON audit_logs(ts);
`

func migrate(db *sql.DB) error {
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	// 存量数据库补列（新建表已含全部列）。每列必须带自己的正确类型：
	// 早先这里只区分 direction/host_key/sort_order，其余一律 TEXT NOT NULL DEFAULT ''，
	// 于是 last_error_at / mute_until / agent_mem 在旧库被建成 TEXT 且旧行值为空串，
	// 新版按 int64 扫描即报 `converting driver.Value type string ("") to a int64`。
	for _, col := range []struct{ name, decl string }{
		{"approved", "INTEGER NOT NULL DEFAULT 1"},
		{"direction", "TEXT NOT NULL DEFAULT 'push'"},
		{"host_key", "TEXT NOT NULL DEFAULT ''"},
		{"sort_order", "INTEGER NOT NULL DEFAULT 0"},
		{"public_ip", "TEXT NOT NULL DEFAULT ''"},
		{"agent_version", "TEXT NOT NULL DEFAULT ''"},
		{"agent_goos", "TEXT NOT NULL DEFAULT ''"},
		{"agent_arch", "TEXT NOT NULL DEFAULT ''"},
		{"last_error", "TEXT NOT NULL DEFAULT ''"},
		{"last_error_at", "INTEGER NOT NULL DEFAULT 0"},
		{"group_tag", "TEXT NOT NULL DEFAULT ''"},
		{"mute_until", "INTEGER NOT NULL DEFAULT 0"},
		{"agent_mem", "INTEGER NOT NULL DEFAULT 0"},
	} {
		var n int
		if err := db.QueryRow(`SELECT COUNT(1) FROM pragma_table_info('nodes') WHERE name=?`, col.name).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			if _, err := db.Exec(`ALTER TABLE nodes ADD COLUMN ` + col.name + ` ` + col.decl); err != nil {
				return err
			}
		}
	}
	if err := migrateNodeNumericTypes(db); err != nil {
		return err
	}
	// 告警规则触发延时
	var d int
	if err := db.QueryRow(`SELECT COUNT(1) FROM pragma_table_info('alert_rules') WHERE name='delay_sec'`).Scan(&d); err != nil {
		return err
	}
	if d == 0 {
		if _, err := db.Exec(`ALTER TABLE alert_rules ADD COLUMN delay_sec INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	// 历史表补列（磁盘 I/O 与每核曲线；新建表已含）
	for _, table := range []string{"metrics_5s", "metrics_1m", "metrics_1h"} {
		for _, col := range []struct {
			name, decl string
		}{{"disk_read", "INTEGER NOT NULL DEFAULT 0"}, {"disk_write", "INTEGER NOT NULL DEFAULT 0"}, {"per_cpu", "TEXT NOT NULL DEFAULT ''"}, {"temp_max", "REAL NOT NULL DEFAULT 0"}} {
			var n int
			if err := db.QueryRow(`SELECT COUNT(1) FROM pragma_table_info('`+table+`') WHERE name=?`, col.name).Scan(&n); err != nil {
				return err
			}
			if n == 0 {
				if _, err := db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + col.name + ` ` + col.decl); err != nil {
					return err
				}
			}
		}
	}
	if err := migrateFiringUnique(db); err != nil {
		return err
	}
	return migrateAPIKeys(db)
}

// migrateNodeNumericTypes 修正存量库中「数值列被建成 TEXT」的节点表。
//
// 旧版补列逻辑对除 direction / host_key / sort_order 之外的列统一用
// TEXT NOT NULL DEFAULT 空串，导致 last_error_at / mute_until / agent_mem
// 以 TEXT 亲和性建立、存量行取值全是空串。新版把它们扫描进 int64，
// 于是所有节点查询都报 converting driver.Value type string ("") to a int64，
// 概览 / 详情 / /metrics 整体 500。
//
// SQLite 改不了列类型，只能重建表；顺手把空串归一为 0（approved 归一为 1），
// 避免 TEXT 亲和性继续把后续写入的数字转成文本。重建是幂等的：
// 列类型已正确时直接返回。
func migrateNodeNumericTypes(db *sql.DB) error {
	numeric := map[string]bool{
		"approved":      true,
		"last_error_at": true,
		"mute_until":    true,
		"agent_mem":     true,
	}
	bad := false
	rows, err := db.Query(`SELECT name, UPPER(type) FROM pragma_table_info('nodes')`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var name, typ string
		if err := rows.Scan(&name, &typ); err != nil {
			rows.Close()
			return err
		}
		if numeric[name] && !strings.Contains(typ, "INT") {
			bad = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if !bad {
		return nil
	}

	// 数值列用 CAST 归一：空串 → 默认值，其余按整数读取。
	cast := func(col, def string) string {
		return `CAST(COALESCE(NULLIF(TRIM(CAST(` + col + ` AS TEXT)),''),'` + def + `') AS INTEGER)`
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, stmt := range []string{
		`CREATE TABLE nodes_migrate ` + nodesTableDef,
		`INSERT INTO nodes_migrate (` + nodeMigrateCols + `) SELECT
			id, name, os_type, access_mode, address, port, token, enabled,
			` + cast("approved", "1") + `,
			direction, report_interval, status, last_seen, created_at, auth_enc, host_key, sort_order,
			public_ip, agent_version, agent_goos, agent_arch, last_error,
			` + cast("last_error_at", "0") + `,
			group_tag,
			` + cast("mute_until", "0") + `,
			` + cast("agent_mem", "0") + `
		FROM nodes`,
		`DROP TABLE nodes`,
		`ALTER TABLE nodes_migrate RENAME TO nodes`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_nodes_token ON nodes(token)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// nodeMigrateCols 重建 nodes 表时搬运的列（与 nodesTableDef 一致）。
const nodeMigrateCols = `id, name, os_type, access_mode, address, port, token, enabled, approved,
	direction, report_interval, status, last_seen, created_at, auth_enc, host_key, sort_order,
	public_ip, agent_version, agent_goos, agent_arch, last_error, last_error_at, group_tag,
	mute_until, agent_mem`

// migrateAPIKeys 把旧版单 Key（settings.api_key_hash / api_key_scope）迁入 api_keys 表，
// 之后统一走多 Key 存储；旧 settings 项随即删除，避免两套来源不一致。
func migrateAPIKeys(db *sql.DB) error {
	var hash string
	err := db.QueryRow(`SELECT value FROM settings WHERE key='api_key_hash'`).Scan(&hash)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if hash != "" {
		var scope string
		_ = db.QueryRow(`SELECT value FROM settings WHERE key='api_key_scope'`).Scan(&scope)
		if scope != "admin" && scope != "view" {
			scope = "view"
		}
		if _, err := db.Exec(`INSERT OR IGNORE INTO api_keys(name, hash, scope, created_at) VALUES('default',?,?,?)`,
			hash, scope, time.Now().Unix()); err != nil {
			return err
		}
	}
	// 迁移后旧键不再使用（旧库首次启动即清理）
	if _, err := db.Exec(`DELETE FROM settings WHERE key IN ('api_key_hash','api_key_scope')`); err != nil {
		return err
	}
	return nil
}

// migrateFiringUnique 保证「同一节点同一指标最多一条 firing 告警」。
// 并发上报时「查 HasFiring → Insert」存在竞态，可能写出重复 firing，
// 用部分唯一索引在数据库层兜底；建索引前先把历史重复记录收敛为 resolved。
func migrateFiringUnique(db *sql.DB) error {
	if _, err := db.Exec(`UPDATE alerts SET status='resolved',
		resolved_at = CASE WHEN resolved_at > 0 THEN resolved_at ELSE CAST(strftime('%s','now') AS INTEGER) END
		WHERE status='firing' AND id NOT IN (
			SELECT keep_id FROM (
				SELECT MIN(id) AS keep_id FROM alerts WHERE status='firing' GROUP BY node_id, metric
			)
		)`); err != nil {
		return err
	}
	_, err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_alert_firing
		ON alerts(node_id, metric) WHERE status='firing'`)
	return err
}
