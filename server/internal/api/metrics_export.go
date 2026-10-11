// 指标导出：Prometheus /metrics 端点 + 历史 CSV 导出。
// Prometheus 文本格式手写（无第三方依赖）；节点最新样本聚合输出。
package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// handleMetricsExport GET /metrics — Prometheus 抓取端点（text/plain; version=0.0.4）。
// 暴露全部已批准节点的最新指标；开启访问密码时同样可访问（Prometheus 无会话），
// 公网部署请在反代层限制来源（见 docs/使用部署手册.md 反代章节）。
func (s *Server) handleMetricsExport(c *gin.Context) {
	nodes, err := s.db.ListNodes()
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	latest, _ := s.db.LatestAll()

	var b strings.Builder
	b.WriteString("# HELP cc_node_up 节点在线状态（1=在线 0=离线）\n")
	b.WriteString("# TYPE cc_node_up gauge\n")
	b.WriteString("# HELP cc_node_cpu_percent CPU 使用率百分比\n")
	b.WriteString("# TYPE cc_node_cpu_percent gauge\n")
	b.WriteString("# HELP cc_node_mem_percent 内存使用率百分比\n")
	b.WriteString("# TYPE cc_node_mem_percent gauge\n")
	b.WriteString("# HELP cc_node_disk_percent 根分区/主数据store 占用百分比\n")
	b.WriteString("# TYPE cc_node_disk_percent gauge\n")
	b.WriteString("# HELP cc_node_load1 1 分钟平均负载\n")
	b.WriteString("# TYPE cc_node_load1 gauge\n")
	b.WriteString("# HELP cc_node_net_rx_bytes_per_second 下行速率 Bytes/s\n")
	b.WriteString("# TYPE cc_node_net_rx_bytes_per_second gauge\n")
	b.WriteString("# HELP cc_node_net_tx_bytes_per_second 上行速率 Bytes/s\n")
	b.WriteString("# TYPE cc_node_net_tx_bytes_per_second gauge\n")
	b.WriteString("# HELP cc_node_uptime_seconds 运行时长秒\n")
	b.WriteString("# TYPE cc_node_uptime_seconds gauge\n")
	b.WriteString("# HELP cc_node_disk_read_bytes_per_second 磁盘读速率 Bytes/s\n")
	b.WriteString("# TYPE cc_node_disk_read_bytes_per_second gauge\n")
	b.WriteString("# HELP cc_node_disk_write_bytes_per_second 磁盘写速率 Bytes/s\n")
	b.WriteString("# TYPE cc_node_disk_write_bytes_per_second gauge\n")

	online := 0
	for _, n := range nodes {
		if !n.Approved {
			continue
		}
		up := 0.0
		if n.Status == "online" || n.Status == "alert" {
			up = 1
			online++
		}
		labels := fmt.Sprintf(`id="%d",name="%s"`, n.ID, promLabel(n.Name))
		sample := latest[n.ID]
		fmt.Fprintf(&b, "cc_node_up{%s} %g\n", labels, up)
		if sample == nil {
			continue
		}
		fmt.Fprintf(&b, "cc_node_cpu_percent{%s} %g\n", labels, sample.CPU)
		fmt.Fprintf(&b, "cc_node_mem_percent{%s} %g\n", labels, sample.MemPct())
		fmt.Fprintf(&b, "cc_node_disk_percent{%s} %g\n", labels, sample.DiskPct)
		fmt.Fprintf(&b, "cc_node_load1{%s} %g\n", labels, sample.Load1)
		fmt.Fprintf(&b, "cc_node_net_rx_bytes_per_second{%s} %d\n", labels, sample.NetRx)
		fmt.Fprintf(&b, "cc_node_net_tx_bytes_per_second{%s} %d\n", labels, sample.NetTx)
		fmt.Fprintf(&b, "cc_node_uptime_seconds{%s} %d\n", labels, sample.Uptime)
		fmt.Fprintf(&b, "cc_node_disk_read_bytes_per_second{%s} %d\n", labels, sample.DiskRead)
		fmt.Fprintf(&b, "cc_node_disk_write_bytes_per_second{%s} %d\n", labels, sample.DiskWrite)
	}
	fmt.Fprintf(&b, "# HELP cc_scrape_nodes_total 抓取到的已批准节点数\n# TYPE cc_scrape_nodes_total gauge\ncc_scrape_nodes_total %d\n", len(nodes))

	c.Header("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	c.String(http.StatusOK, b.String())
}

// promLabel 转义 Prometheus label 值中的反斜杠、引号与换行。
func promLabel(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return s
}

// csvFloat 稳定的小数格式（CSV 导出用）。
func csvFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}
