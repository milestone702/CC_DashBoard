// 告警引擎：阈值评估（上报时同步触发）与离线巡检（周期任务）。
package alert

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"cc_dashboard/server/internal/model"
	"cc_dashboard/server/internal/notify"
	"cc_dashboard/server/internal/store"
)

// Engine 告警评估器。
type Engine struct {
	db *store.DB
	// breachStart 记录各节点指标「连续超阈值」的起始时刻（触发延时用，内存态）。
	// 由上报 handler（多个 HTTP 协程）与采集协程并发访问，所有读写必须持 mu。
	mu          sync.Mutex
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
			e.clearBreach(key)
			if firing {
				if changed, err := e.db.ResolveAlert(nodeID, r.Metric); err == nil && changed {
					e.broadcast(map[string]any{"type": "alert_resolved", "node_id": nodeID, "metric": r.Metric})
					e.notifyNode(nodeID, "告警恢复",
						fmt.Sprintf("指标：%s\n当前值：%s，已回到阈值内", metricCN(r.Metric), fmtMetric(r.Metric, value)))
				}
			}
			continue
		}

		// 触发延时：记录起始 / 等待到期
		if r.DelaySec > 0 && !e.breachReached(key, now, int64(r.DelaySec)) {
			continue
		}

		if !firing {
			level := "warning"
			if value >= 95 {
				level = "critical"
			}
			created, err := e.db.InsertAlert(nodeID, r.Metric, level, round1(value))
			if err != nil {
				log.Printf("告警写入失败 node=%d: %v", nodeID, err)
				continue
			}
			if !created {
				continue // 并发/竞态下已有 firing 记录，不重复通知
			}
			e.broadcast(map[string]any{
				"type": "alert_firing", "node_id": nodeID, "metric": r.Metric,
				"level": level, "value": round1(value),
			})
			content := fmt.Sprintf("指标：%s\n当前值：%s（规则 %s %s）\n级别：%s",
				metricCN(r.Metric), fmtMetric(r.Metric, value), r.Op, fmtMetric(r.Metric, r.Threshold), level)
			if r.DelaySec > 0 {
				content += fmt.Sprintf("\n触发延时：%d 分钟（连续超阈值后触发）", r.DelaySec/60)
			}
			e.notifyNode(nodeID, "告警触发", content)
		}
	}
}

// ResolveOffline 清除节点的离线告警（上线时调用）。
func (e *Engine) ResolveOffline(nodeID int64) {
	if changed, err := e.db.ResolveAlert(nodeID, "offline"); err == nil && changed {
		e.broadcast(map[string]any{"type": "alert_resolved", "node_id": nodeID, "metric": "offline"})
		e.notifyNode(nodeID, "节点恢复在线", "离线告警已解除")
	}
}

// breachReached 维护「连续超阈值」起点并判断是否已满 waitSec。
// 返回 false 表示仍在等待（首个采样或时长未达标）。
func (e *Engine) breachReached(key string, now, waitSec int64) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	start, ok := e.breachStart[key]
	if !ok {
		e.breachStart[key] = now
		return false
	}
	return now-start >= waitSec
}

// clearBreach 清除某指标的连续计时（指标回落时）。
func (e *Engine) clearBreach(key string) {
	e.mu.Lock()
	delete(e.breachStart, key)
	e.mu.Unlock()
}

// Forget 丢弃指定节点的计时状态（节点删除时调用，避免内存态长期残留）。
func (e *Engine) Forget(nodeID int64) {
	prefix := strconv.FormatInt(nodeID, 10) + ":"
	e.mu.Lock()
	for k := range e.breachStart {
		if strings.HasPrefix(k, prefix) {
			delete(e.breachStart, k)
		}
	}
	e.mu.Unlock()
}

// SetOnline 节点上线：解除离线告警并推送状态。
// 上线/离线状态变化唯一入口，采集入库与 Agent 上报两处调用。
func (e *Engine) SetOnline(nodeID int64) {
	e.ResolveOffline(nodeID)
	e.broadcastStatus(nodeID, model.StatusOnline)
}

// broadcast 推送 WS 消息（回调未注入时跳过）。
func (e *Engine) broadcast(v map[string]any) {
	if e.Broadcast != nil {
		e.Broadcast(v)
	}
}

// broadcastStatus 推送 node_status 消息。
func (e *Engine) broadcastStatus(nodeID int64, status string) {
	e.broadcast(map[string]any{"type": "node_status", "node_id": nodeID, "status": status})
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
			if created, err := e.db.InsertAlert(n.ID, "offline", "critical", float64(now-n.LastSeen)); err == nil && created {
				e.broadcast(map[string]any{
					"type": "alert_firing", "node_id": n.ID, "metric": "offline",
					"level": "critical", "value": float64(now - n.LastSeen),
				})
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
		// 静默期（维护窗口）内：告警照常记录，但不发通知，避免重启/升级时刷屏
		if n.MuteUntil > time.Now().Unix() {
			return
		}
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
	case "load1":
		return "1 分钟负载"
	case "net_rx":
		return "下行速率"
	case "net_tx":
		return "上行速率"
	case "temp":
		return "温度"
	case "offline":
		return "离线"
	default:
		return m
	}
}

// fmtMetric 按指标量纲格式化数值（百分比 / 负载 / Bytes/s）。
func fmtMetric(metric string, v float64) string {
	switch metric {
	case "load1":
		return fmt.Sprintf("%.2f", v)
	case "net_rx", "net_tx":
		return fmtRateText(v)
	case "temp":
		return fmt.Sprintf("%.1f℃", v)
	default:
		return fmt.Sprintf("%.2f%%", v)
	}
}

// fmtRateText Bytes/s → 人类可读（告警文案用）。
func fmtRateText(v float64) string {
	units := []string{"B/s", "KB/s", "MB/s", "GB/s", "TB/s"}
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	if i == 0 || v >= 100 {
		return fmt.Sprintf("%.0f %s", v, units[i])
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}

func metricValue(s *model.MetricsSample, metric string) (float64, bool) {
	switch metric {
	case "cpu":
		return s.CPU, true
	case "mem_pct":
		return s.MemUsedPct(), true
	case "disk_pct":
		return s.DiskPct, true
	case "load1":
		return s.Load1, true
	case "net_rx":
		return float64(s.NetRx), true
	case "net_tx":
		return float64(s.NetTx), true
	case "temp":
		return s.TempMax, true
	default:
		return 0, false
	}
}

func round1(v float64) float64 {
	return float64(int64(v*10+0.5)) / 10
}
