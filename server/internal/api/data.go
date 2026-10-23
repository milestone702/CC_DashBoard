// GET /summary、GET /nodes/:id/history — 聚合与历史查询。
package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"cc_dashboard/server/internal/model"
	"cc_dashboard/server/internal/store"
)

// handleSummary 首页全局聚合：计数 + firing 告警列表。
func (s *Server) handleSummary(c *gin.Context) {
	total, online, err := s.db.CountNodesByStatus()
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	alerting, _ := s.db.CountAlerting()
	alerts, _ := s.db.ListAlerts("firing", 20)

	c.JSON(http.StatusOK, gin.H{
		"total":     total,
		"online":    online,
		"offline":   total - online,
		"alerting":  alerting,
		"ws_clients": s.hub.ClientCount(),
		"alerts":    alerts,
	})
}

// rollupThrottled 查询侧补齐降采样窗口，最多每 30 秒执行一次。
// 后台 rollupLoop 已每 30 秒聚合，这里只为「刚上报的数据立即可见」服务；
// 若每个查询请求都同步聚合（33 个窗口 × 写盘），单连接下会明显拖慢响应。
func (s *Server) rollupThrottled(now int64) {
	if !s.rollupMu.TryLock() {
		return // 已有协程在聚合，本次直接读（数据最多滞后一个周期）
	}
	defer s.rollupMu.Unlock()
	if now-s.lastRollup < 30 {
		return
	}
	s.lastRollup = now
	s.db.RollupFresh(now)
}

// handleHistory 历史时序查询。
// range 取值：1h | 24h | 7d | 30d | 180d（半年）| 365d（一年），默认 24h。
// 数据来源：1h→metrics_5s，24h/7d→metrics_1m，30d/180d/365d→metrics_1h。
func (s *Server) handleHistory(c *gin.Context) {
	n, ok := s.nodeByID(c)
	if !ok {
		return
	}
	now := time.Now().Unix()
	rng := c.DefaultQuery("range", "24h")
	table, from, interval, okRange := store.ResolveRange(rng, now)
	if !okRange {
		fail(c, http.StatusBadRequest, "unsupported range: use 1h|24h|7d|30d|180d|365d")
		return
	}
	s.rollupThrottled(now) // 查询前补齐聚合，消除降采样延迟（限流，见 rollupThrottled）
	points, err := s.db.History(n.ID, table, from, now)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	if points == nil {
		points = []*model.HistoryPoint{} // 空数组而非 null
	}

	// CSV 导出（?format=csv），Excel 可直接打开
	if c.Query("format") == "csv" {
		var b strings.Builder
		b.WriteString("ts,datetime,cpu_pct,mem_used,mem_total,disk_pct,net_rx_bps,net_tx_bps,load1,uptime_sec,disk_read_bps,disk_write_bps,per_cpu\n")
		for _, p := range points {
			dt := time.Unix(p.TS, 0).Format("2006-01-02 15:04:05")
			b.WriteString(strings.Join([]string{
				strconv.FormatInt(p.TS, 10), dt,
				csvFloat(p.CPU),
				strconv.FormatInt(p.MemUsed, 10), strconv.FormatInt(p.MemTotal, 10),
				csvFloat(p.DiskPct),
				strconv.FormatInt(p.NetRx, 10), strconv.FormatInt(p.NetTx, 10),
				csvFloat(p.Load1), strconv.FormatInt(p.Uptime, 10),
				strconv.FormatInt(p.DiskRead, 10), strconv.FormatInt(p.DiskWrite, 10),
				`"` + strings.ReplaceAll(p.PerCPU, `"`, `""`) + `"`,
			}, ",") + "\n")
		}
		c.Header("Content-Type", "text/csv; charset=utf-8")
		c.Header("Content-Disposition",
			fmt.Sprintf(`attachment; filename="node%d_%s.csv"`, n.ID, rng))
		c.String(http.StatusOK, b.String())
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"range":        rng,
		"source_table": table,
		"interval_sec": interval,
		"from":         from,
		"to":           now,
		"points":       points,
	})
}
