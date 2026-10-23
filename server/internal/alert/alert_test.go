package alert

import (
	"path/filepath"
	"sync"
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

// TestEvaluateSampleConcurrent 并发评估不得 panic / data race。
// 回归点：breachStart 曾是无锁 map，多个上报协程同时 delete/write 会触发
// “fatal error: concurrent map writes” 直接终止进程。
// 用 go test -race 跑本测试才有实际意义。
func TestEvaluateSampleConcurrent(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	id, err := db.CreateNode(&model.Node{
		Name: "nc", OSType: model.OSTypeLinux, AccessMode: model.ModeAgent,
		Token: "tc", Enabled: true, Approved: true, ReportInterval: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	// DelaySec>0 覆盖 breachStart 写入分支；阈值高低交替覆盖 delete 分支
	if _, err := db.CreateRule(&model.AlertRule{
		NodeID: 0, Metric: "cpu", Op: ">", Threshold: 50, DelaySec: 60, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	e := NewEngine(db)
	const workers = 8
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 30; i++ {
				cpu := 40.0 // 未超阈值 → clearBreach
				if (i+w)%2 == 0 {
					cpu = 90 // 超阈值 → breachReached 写 map
				}
				e.EvaluateSample(id, &model.MetricsSample{CPU: cpu, MemTotal: 100, MemUsed: int64(cpu)})
			}
		}(w)
	}
	wg.Wait()

	// 并发下同一指标最多一条 firing（依赖 DB 唯一索引兜底）
	var n int
	if err := db.QueryRow(`SELECT COUNT(1) FROM alerts WHERE node_id=? AND metric='cpu' AND status='firing'`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n > 1 {
		t.Fatalf("并发评估产生了 %d 条 firing 告警，应为 1", n)
	}
}
