// 告警引擎：阈值评估（上报时同步触发）与离线巡检（周期任务）。
package alert

import (
	"fmt"
	"log"
	"time"

	"cc_dashboard/server/internal/model"
	"cc_dashboard/server/internal/notify"
	"cc_dashboard/server/internal/store"
)

// Engine 告警评估器。
type Engine struct {
	db *store.DB
	// breachStart 记录各节点指标「连续超阈值」的起始时刻（触发延时用，内存态）
	breachStart map[string]int64
	// Broadcast 上下线状态推送回调（由 main 注入 hub.Broadcast）
	Broadcast func(any)
}

// nowFunc 可注入时钟（单元测试用）。
var nowFunc = time.Now

// NewEngine 创建告警引擎。
func NewEngine(db *store.DB) *Engine {
	return &Engine{db: db, breachStart: map[string]int64{}}
}

// Start 启动离线巡检（每 10 秒）。
func (e *Engine) Start() {
	e.scanOffline()
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for range t.C {
		e.scanOffline()
	}
}

// EvaluateSample 在每次上报后同步评估该节点的阈值规则（开发文档 7.4）。
// 支持触发延时 delay_sec：指标必须「连续」超阈值满 N 秒才告警；任一采样回落即重新计时。
func (e *Engine) EvaluateSample(nodeID int64, s *model.MetricsSample) {
	rules, err := e.db.RulesForNode(nodeID)
	if err != nil {
		log.Printf("告警规则查询失败 node=%d: %v", nodeID, err)
		return
	}
	now := nowFunc().Unix()
	for _, r := range rules {
		value, ok := metricValue(s, r.Metric)
		if !ok {
			continue
		}
		breached := false
		switch r.Op {
		case ">=":
			breached = value >= r.Threshold
		default: // ">"
			breached = value > r.Threshold
		}
		firing, err := e.db.HasFiring(nodeID, r.Metric)
		if err != nil {
			continue
		}
		key := fmt.Sprintf("%d:%s", nodeID, r.Metric)

		if !breached {
			// 回落：清除连续计时并恢复
			delete(e.breachStart, key)
			if firing {
				if err := e.db.ResolveAlert(nodeID, r.Metric); err == nil {
					e.notifyNode(nodeID, "告警恢复",
						fmt.Sprintf("指标：%s\n当前值：%.2f%%，已回到阈值内", metricCN(r.Metric), value))
				}
			}
			continue
		}

		// 触发延时：记录起始 / 等待到期
		if r.DelaySec > 0 {
			start, exists := e.breachStart[key]
			if !exists {
				e.breachStart[key] = now
				continue // 第一个超阈值采样，开始计时
			}
			if now-start < int64(r.DelaySec) {
				continue // 连续时长未达标
			}
		}

		if !firing {
			level := "warning"
			if value >= 95 {
				level = "critical"
			}
			if err := e.db.InsertAlert(nodeID, r.Metric, level, round1(value)); err != nil {
				log.Printf("告警写入失败 node=%d: %v", nodeID, err)
				continue
			}
			content := fmt.Sprintf("指标：%s\n当前值：%.2f%%（规则 %s %.2f%%）\n级别：%s",
				metricCN(r.Metric), value, r.Op, r.Threshold, level)
			if r.DelaySec > 0 {
				content += fmt.Sprintf("\n触发延时：%d 分钟（连续超阈值后触发）", r.DelaySec/60)
			}
			e.notifyNode(nodeID, "告警触发", content)
		}
	}
}

// ResolveOffline 清除节点的离线告警（上线时调用）。
func (e *Engine) ResolveOffline(nodeID int64) {
	if err := e.db.ResolveAlert(nodeID, "offline"); err == nil {
		e.notifyNode(nodeID, "节点恢复在线", "离线告警已解除")
	}
}

// SetOnline 节点上线：解除离线告警并推送状态。
// 上线/离线状态变化唯一入口，采集入库与 Agent 上报两处调用。
func (e *Engine) SetOnline(nodeID int64) {
	e.ResolveOffline(nodeID)
	e.broadcastStatus(nodeID, model.StatusOnline)
}

// broadcastStatus 推送 node_status 消息（回调未注入时跳过）。
func (e *Engine) broadcastStatus(nodeID int64, status string) {
	if e.Broadcast != nil {
		e.Broadcast(map[string]any{"type": "node_status", "node_id": nodeID, "status": status})
	}
}

func (e *Engine) scanOffline() {
	now := time.Now().Unix()
	nodes, err := e.db.OfflineCandidates(now)
	if err != nil {
		log.Printf("离线巡检查询失败: %v", err)
		return
	}
	for _, n := range nodes {
		threshold := int64(n.ReportInterval) * 3
		if threshold < 15 {
			threshold = 15
		}
		if now-n.LastSeen < threshold {
			continue
		}
		if err := e.db.MarkNodeOffline(n.ID); err != nil {
			log.Printf("标记离线失败 node=%d: %v", n.ID, err)
			continue
		}
		e.broadcastStatus(n.ID, model.StatusOffline)
		has, _ := e.db.HasFiring(n.ID, "offline")
		if !has {
			if err := e.db.InsertAlert(n.ID, "offline", "critical", float64(now-n.LastSeen)); err == nil {
				e.notifyNode(n.ID, "节点离线",
					fmt.Sprintf("超过 %d 秒未上报（阈值 3×上报周期）", now-n.LastSeen))
			}
		}
	}
}

// notifyNode 按节点名组装标题并发送通知（配置未启用时内部自动跳过）。
func (e *Engine) notifyNode(nodeID int64, action, content string) {
	name := fmt.Sprintf("节点#%d", nodeID)
	if n, err := e.db.GetNode(nodeID); err == nil {
		name = n.Name
	}
	notify.Fire(e.db, fmt.Sprintf("%s · %s", action, name), content+"\n时间："+time.Now().Format("2006-01-02 15:04:05"))
}

func metricCN(m string) string {
	switch m {
	case "cpu":
		return "CPU"
	case "mem_pct":
		return "内存"
	case "disk_pct":
		return "磁盘"
	case "offline":
		return "离线"
	default:
		return m
	}
}

func metricValue(s *model.MetricsSample, metric string) (float64, bool) {
	switch metric {
	case "cpu":
		return s.CPU, true
	case "mem_pct":
		return s.MemPct(), true
	case "disk_pct":
		return s.DiskPct, true
	default:
		return 0, false
	}
}

func round1(v float64) float64 {
	return float64(int64(v*10+0.5)) / 10
}
