package store

import (
	"path/filepath"
	"testing"

	"cc_dashboard/server/internal/model"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestResolveRange(t *testing.T) {
	now := int64(1_800_000_000)
	cases := []struct {
		in    string
		table string
		ok    bool
	}{
		{"1h", Table5s, true},
		{"24h", Table1m, true},
		{"7d", Table1m, true},
		{"30d", Table1h, true},
		{"180d", Table1h, true},
		{"365d", Table1h, true},
		{"6mo", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		table, from, interval, ok := ResolveRange(c.in, now)
		if ok != c.ok || table != c.table {
			t.Errorf("ResolveRange(%q) = (%q, ok=%v), want (%q, ok=%v)", c.in, table, ok, c.table, c.ok)
		}
		if ok && from >= now {
			t.Errorf("ResolveRange(%q) from=%d 应早于 now", c.in, from)
		}
		if ok && interval <= 0 {
			t.Errorf("ResolveRange(%q) interval 非法", c.in)
		}
	}
}

func TestInsertAggregateHistory(t *testing.T) {
	db := openTestDB(t)

	id, err := db.CreateNode(&model.Node{
		Name: "n1", OSType: model.OSTypeLinux, AccessMode: model.ModeAgent,
		Token: "tok1", Enabled: true, ReportInterval: 5,
	})
	if err != nil {
		t.Fatalf("create node: %v", err)
	}

	// 对齐到分钟起点，写入 12 条 5s 样本（cpu 10..121；带磁盘 I/O 与每核）
	base := int64(1_700_000_040) // 已对齐 60 的倍数
	for i := 0; i < 12; i++ {
		s := &model.MetricsSample{
			CPU: float64(10 + i*10), MemUsed: 512 << 20, MemTotal: 1 << 30,
			DiskPct: 50, NetRx: 1000, NetTx: 500, Load1: 0.5, Uptime: 100,
			DiskRead: int64(1000 + i*100), DiskWrite: int64(2000 + i*100),
			PerCPU: []float64{float64(i), float64(i + 1)},
		}
		if err := db.insertSampleAt(id, base+int64(i*5), s); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	// 降采样：窗口 [base, base+60)
	if err := db.AggregateUp(Table5s, Table1m, 60, base, base+60); err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	// 幂等：重复执行不报错、不翻倍
	if err := db.AggregateUp(Table5s, Table1m, 60, base, base+60); err != nil {
		t.Fatalf("aggregate repeat: %v", err)
	}

	pts, err := db.History(id, Table1m, base-60, base+120)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(pts) != 1 {
		t.Fatalf("1m 点数 = %d, want 1", len(pts))
	}
	// avg(10,20,...,120) = 65
	if pts[0].CPU < 64.9 || pts[0].CPU > 65.1 {
		t.Errorf("1m cpu avg = %v, want 65", pts[0].CPU)
	}
	if pts[0].TS != base {
		t.Errorf("1m ts = %d, want %d", pts[0].TS, base)
	}
	// 磁盘 I/O AVG 落聚合表
	if pts[0].DiskRead < 1450 || pts[0].DiskRead > 1550 {
		t.Errorf("1m disk_read avg = %d, want ~1500", pts[0].DiskRead)
	}
	if pts[0].DiskWrite < 2450 || pts[0].DiskWrite > 2550 {
		t.Errorf("1m disk_write avg = %d, want ~2500", pts[0].DiskWrite)
	}
	// per_cpu 取窗口内首个非空样本（首条 i=0 → [0,1]）
	if pts[0].PerCPU == "" {
		t.Fatal("1m per_cpu 应非空")
	}
	if pts[0].PerCPU != "[0,1]" {
		t.Errorf("1m per_cpu = %s, want [0,1]（窗口首点）", pts[0].PerCPU)
	}

	// 1m → 1h
	if err := db.AggregateUp(Table1m, Table1h, 3600, base-3600, base+3600); err != nil {
		t.Fatalf("aggregate hour: %v", err)
	}
	hours, err := db.History(id, Table1h, base-3600, base+3600)
	if err != nil {
		t.Fatalf("history hour: %v", err)
	}
	if len(hours) != 1 {
		t.Fatalf("1h 点数 = %d, want 1", len(hours))
	}

	// 最新样本
	latest, err := db.LatestSample(id)
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if latest == nil || latest.CPU != 120 {
		t.Fatalf("latest = %+v, want cpu=120", latest)
	}
	if latest.DiskRead != 2100 || latest.DiskWrite != 3100 {
		t.Errorf("latest io = %d/%d, want 2100/3100", latest.DiskRead, latest.DiskWrite)
	}
	if len(latest.PerCPU) != 2 || latest.PerCPU[0] != 11 || latest.PerCPU[1] != 12 {
		t.Errorf("latest per_cpu = %v, want [11 12]", latest.PerCPU)
	}
}

func TestNodeStatusAndAlerts(t *testing.T) {
	db := openTestDB(t)
	id, err := db.CreateNode(&model.Node{
		Name: "n2", OSType: model.OSTypeLinux, AccessMode: model.ModeAgent,
		Token: "tok2", Enabled: true, ReportInterval: 5,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// 上线（状态变更 → 事件）
	changed, err := db.SetNodeOnline(id)
	if err != nil || !changed {
		t.Fatalf("online changed=%v err=%v", changed, err)
	}
	// 重复上线不产生新事件
	changed, _ = db.SetNodeOnline(id)
	if changed {
		t.Fatal("重复上线不应算状态变更")
	}

	// 阈值告警 fire → resolve
	if err := db.InsertAlert(id, "cpu", "warning", 95.5); err != nil {
		t.Fatalf("insert alert: %v", err)
	}
	firing, _ := db.FiringAlertNodes()
	if !firing[id] {
		t.Fatal("firing 节点集合应包含该节点")
	}
	if err := db.ResolveAlert(id, "cpu"); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	firing, _ = db.FiringAlertNodes()
	if firing[id] {
		t.Fatal("resolve 后不应仍在 firing 集合")
	}

	// 离线候选：超时未上报的在线节点入选；last_seen=0（从未上报）不入选
	if _, err := db.Exec(`UPDATE nodes SET last_seen=1 WHERE id=?`, id); err != nil {
		t.Fatalf("set last_seen: %v", err)
	}
	cands, err := db.OfflineCandidates(100)
	if err != nil || len(cands) != 1 || cands[0].ID != id {
		t.Fatalf("cands = %+v err=%v, want [%d]", cands, err, id)
	}
	id2, err := db.CreateNode(&model.Node{
		Name: "never-seen", OSType: model.OSTypeLinux, AccessMode: model.ModeAgent,
		Token: "tok3", Enabled: true, ReportInterval: 5,
	})
	if err != nil {
		t.Fatalf("create node2: %v", err)
	}
	if _, err := db.Exec(`UPDATE nodes SET status='online', last_seen=0 WHERE id=?`, id2); err != nil {
		t.Fatalf("set status: %v", err)
	}
	cands, _ = db.OfflineCandidates(100)
	for _, n := range cands {
		if n.ID == id2 {
			t.Fatal("从未上报的节点不应进离线巡检")
		}
	}

	if err := db.MarkNodeOffline(id); err != nil {
		t.Fatalf("offline: %v", err)
	}
	events, _ := db.RecentEvents(id, 10)
	if len(events) != 2 { // online + offline
		t.Fatalf("events = %d, want 2", len(events))
	}
}
