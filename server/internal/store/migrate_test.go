package store

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"cc_dashboard/server/internal/model"
)

// legacyNodesSchema 复现旧版补列逻辑留下的表结构：
// last_error_at / mute_until / agent_mem 被写成 TEXT NOT NULL DEFAULT 空串。
const legacyNodesSchema = `
CREATE TABLE nodes (
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
  auth_enc        TEXT    NOT NULL DEFAULT '',
  host_key        TEXT    NOT NULL DEFAULT '',
  sort_order      INTEGER NOT NULL DEFAULT 0,
  public_ip       TEXT    NOT NULL DEFAULT '',
  agent_version   TEXT    NOT NULL DEFAULT '',
  agent_goos      TEXT    NOT NULL DEFAULT '',
  agent_arch      TEXT    NOT NULL DEFAULT '',
  last_error      TEXT    NOT NULL DEFAULT '',
  last_error_at   TEXT    NOT NULL DEFAULT '',
  group_tag       TEXT    NOT NULL DEFAULT '',
  mute_until      TEXT    NOT NULL DEFAULT '',
  agent_mem       TEXT    NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX idx_nodes_token ON nodes(token);
INSERT INTO nodes (name, token, approved) VALUES ('old-node', 'tok-old', 1);
`

// TestMigrateFixesTextNumericCols 旧库升级后节点必须能被正常扫描。
// 曾因数值列为 TEXT 且值为空串，Scan 进 int64 失败，节点列表 / 详情 / /metrics 全部 500。
func TestMigrateFixesTextNumericCols(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")

	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open legacy: %v", err)
	}
	if _, err := raw.Exec(legacyNodesSchema); err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close legacy: %v", err)
	}

	db, err := Open(path)
	if err != nil {
		t.Fatalf("open with migration: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	nodes, err := db.ListNodes()
	if err != nil {
		t.Fatalf("ListNodes after migration: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("nodes = %d, want 1", len(nodes))
	}
	n := nodes[0]
	if n.Name != "old-node" || n.Token != "tok-old" {
		t.Fatalf("节点内容丢失: %+v", n)
	}
	if n.LastErrorAt != 0 || n.MuteUntil != 0 || n.AgentMem != 0 {
		t.Fatalf("数值列未归零: last_error_at=%d mute_until=%d agent_mem=%d",
			n.LastErrorAt, n.MuteUntil, n.AgentMem)
	}
	if !n.Approved {
		t.Fatalf("approved 应保持为已批准")
	}

	// 列亲和性已修正为 INTEGER（否则后续写入的数字还会被存成文本）
	rows, err := db.Query(`SELECT name, UPPER(type) FROM pragma_table_info('nodes')`)
	if err != nil {
		t.Fatalf("pragma_table_info: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name, typ string
		if err := rows.Scan(&name, &typ); err != nil {
			t.Fatalf("scan pragma: %v", err)
		}
		switch name {
		case "last_error_at", "mute_until", "agent_mem":
			if !strings.Contains(typ, "INT") {
				t.Errorf("%s 类型 = %s, want INTEGER", name, typ)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	// 重建后写入新值仍可读回（唯一索引与自增 ID 未受损）
	if _, err := db.CreateNode(&model.Node{Name: "new-node", Token: "tok-new", Enabled: true}); err != nil {
		t.Fatalf("create after migration: %v", err)
	}
	if err := db.SetNodeError(n.ID, "boom"); err != nil {
		t.Fatalf("SetNodeError: %v", err)
	}
	got, err := db.GetNode(n.ID)
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if got.LastError != "boom" || got.LastErrorAt == 0 {
		t.Fatalf("last_error 写回异常: %q %d", got.LastError, got.LastErrorAt)
	}
}
