// 建表迁移。表结构依据《CC_DashBoard_开发文档》7.2。
package store

import "database/sql"

const schema = `
CREATE TABLE IF NOT EXISTS nodes (
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
  public_ip       TEXT    NOT NULL DEFAULT '',
  agent_version   TEXT    NOT NULL DEFAULT '',
  agent_goos      TEXT    NOT NULL DEFAULT '',
  agent_arch      TEXT    NOT NULL DEFAULT ''
);
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
`

func migrate(db *sql.DB) error {
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	// 存量数据库补列（新建表已含 approved / direction / host_key / sort_order）
	for _, col := range []string{"approved", "direction", "host_key", "sort_order",
		"public_ip", "agent_version", "agent_goos", "agent_arch"} {
		var n int
		if err := db.QueryRow(`SELECT COUNT(1) FROM pragma_table_info('nodes') WHERE name=?`, col).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			def := "INTEGER NOT NULL DEFAULT 1"
			if col == "direction" {
				def = "TEXT NOT NULL DEFAULT 'push'"
			} else if col == "host_key" {
				def = "TEXT NOT NULL DEFAULT ''"
			} else if col == "sort_order" {
				def = "INTEGER NOT NULL DEFAULT 0"
			} else {
				def = "TEXT NOT NULL DEFAULT ''"
			}
			if _, err := db.Exec(`ALTER TABLE nodes ADD COLUMN ` + col + ` ` + def); err != nil {
				return err
			}
		}
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
		}{{"disk_read", "INTEGER NOT NULL DEFAULT 0"}, {"disk_write", "INTEGER NOT NULL DEFAULT 0"}, {"per_cpu", "TEXT NOT NULL DEFAULT ''"}} {
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
	return nil
}
