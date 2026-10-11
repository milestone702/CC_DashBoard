package alert

import (
	"path/filepath"
	"testing"
	"time"

	"cc_dashboard/server/internal/model"
	"cc_dashboard/server/internal/store"
)

func TestTriggerDelay(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	id, err := db.CreateNode(&model.Node{
		Name: "n1", OSType: model.OSTypeLinux, AccessMode: model.ModeAgent,
		Token: "t", Enabled: true, Approved: true, ReportInterval: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	// 规则：cpu > 80%，连续 300 秒（5 分钟）才触发
	if _, err := db.CreateRule(&model.AlertRule{
		NodeID: 0, Metric: "cpu", Op: ">", Threshold: 80,
		DelaySec: 300, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	e := NewEngine(db)
	base := time.Now()
	current := base
	nowFunc = func() time.Time { return current }
	defer func() { nowFunc = time.Now }()

	sample := &model.MetricsSample{CPU: 95}

	// t0：首次超阈值 → 开始计时，不告警
	current = base
	e.EvaluateSample(id, sample)
	if firing, _ := db.HasFiring(id, "cpu"); firing {
		t.Fatal("t0 不应告警")
	}

	// t0+60s：仍在计时 → 不告警
	current = base.Add(60 * time.Second)
	e.EvaluateSample(id, sample)
	if firing, _ := db.HasFiring(id, "cpu"); firing {
		t.Fatal("60s < 300s 不应告警")
	}

	// 中途回落 → 计时清零
	current = base.Add(90 * time.Second)
	e.EvaluateSample(id, &model.MetricsSample{CPU: 10})
	if firing, _ := db.HasFiring(id, "cpu"); firing {
		t.Fatal("回落不应告警")
	}
	// 再次超阈值 → 从头计时
	current = base.Add(100 * time.Second)
	e.EvaluateSample(id, sample)
	if firing, _ := db.HasFiring(id, "cpu"); firing {
		t.Fatal("重新计时起点不应告警")
	}

	// t0+100s+300s：连续满 5 分钟 → 触发
	current = base.Add(400 * time.Second)
	e.EvaluateSample(id, sample)
	if firing, _ := db.HasFiring(id, "cpu"); !firing {
		t.Fatal("连续 300s 后应告警")
	}

	// 持续超阈值 → 不重复告警
	alerts, _ := db.ListAlerts("firing", 10)
	if len(alerts) != 1 {
		t.Fatalf("firing 数量 = %d, want 1", len(alerts))
	}

	// 恢复 → resolved
	current = base.Add(410 * time.Second)
	e.EvaluateSample(id, &model.MetricsSample{CPU: 5})
	if firing, _ := db.HasFiring(id, "cpu"); firing {
		t.Fatal("恢复后不应 firing")
	}
}

func TestDelayZeroFiresImmediately(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	id, _ := db.CreateNode(&model.Node{
		Name: "n2", OSType: model.OSTypeLinux, AccessMode: model.ModeAgent,
		Token: "t2", Enabled: true, Approved: true, ReportInterval: 5,
	})
	_, _ = db.CreateRule(&model.AlertRule{
		NodeID: 0, Metric: "cpu", Op: ">", Threshold: 80, DelaySec: 0, Enabled: true,
	})
	e := NewEngine(db)
	e.EvaluateSample(id, &model.MetricsSample{CPU: 99})
	if firing, _ := db.HasFiring(id, "cpu"); !firing {
		t.Fatal("delay=0 应立即告警")
	}
}
