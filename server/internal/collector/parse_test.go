package collector

import (
	"testing"
	"time"
)

const sampleLinux = `##CPU##
cpu  1000 0 1000 46000 1000 0 100 0 0 0
##PERCPU##
cpu0  500 0 500 23000 500 0 50 0 0 0
cpu1  500 0 500 23000 500 0 50 0 0 0
##MEM##
MemTotal:        8000000 kB
MemAvailable:    4000000 kB
MemFree:         3000000 kB
Buffers:          100000 kB
Cached:          2000000 kB
SwapTotal:       1000000 kB
SwapFree:         500000 kB
##LOAD##
0.50 0.40 0.30 1/200 1234
##UP##
123456.78 234567.89
##NET##
Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo frame compressed
    lo: 1000 0 0 0 0 0 0 0 1000 0 0 0 0 0 0 0
  eth0: 500000 0 0 0 0 0 0 0 200000 0 0 0 0 0 0 0
##DF##
/dev/sda1 10239972 5120000 5119972 51% /
##TEMP##
x86_pkg_temp 45000
acpi-thermal 38000
##PROC##
  1234 java           12.3  4.5
  5678 nginx           3.1  1.2
##HOST##
myhost
Linux 6.6.32
`

func newTestManager() *Manager {
	return &Manager{
		prevCPU:      map[int64]cpuSample{},
		prevPerCores: map[int64][]cpuSample{},
		prevNet:      map[int64]netSample{},
	}
}

func TestParseLinuxFirstPoll(t *testing.T) {
	m := newTestManager()
	now := time.Now()
	s, detail, live, err := m.parseLinux(1, sampleLinux, now)
	if err != nil {
		t.Fatal(err)
	}
	// 首轮：开机以来均值 (1000+0+1000+46000+1000+0+100)=50100 total, idle=46000+1000=47000 → busy=3100 → ~6.19%
	if s.CPU <= 0 || s.CPU >= 100 {
		t.Errorf("cpu = %v, 应为开机以来有效百分比", s.CPU)
	}
	if s.MemTotal != 8000000*1024 {
		t.Errorf("mem_total = %d", s.MemTotal)
	}
	if s.MemUsed != 4000000*1024 {
		t.Errorf("mem_used = %d, want total-avail", s.MemUsed)
	}
	if s.Load1 != 0.5 {
		t.Errorf("load1 = %v", s.Load1)
	}
	if s.Uptime != 123456 {
		t.Errorf("uptime = %d", s.Uptime)
	}
	if s.DiskPct != 51 {
		t.Errorf("disk_pct = %v, want 51", s.DiskPct)
	}
	if s.NetRx != 0 {
		t.Errorf("首轮无差分应为0, got %d", s.NetRx)
	}
	if detail["hostname"] != "myhost" {
		t.Errorf("hostname = %v", detail["hostname"])
	}
	// live 基础块
	if live["cpu_total"] == nil || live["per_cpu"] == nil {
		t.Errorf("live 缺少 cpu_total/per_cpu: %v", live)
	}
	if per, ok := live["per_cpu"].([]float64); !ok || len(per) != 2 {
		t.Errorf("per_cpu = %v, want 2 核", live["per_cpu"])
	}
	if mem, ok := live["mem"].(map[string]any); !ok || mem["buffers"] != int64(100000*1024) {
		t.Errorf("live.mem.buffers = %v", live["mem"])
	}
}

func TestParseLinuxLiveTempsProcs(t *testing.T) {
	m := newTestManager()
	now := time.Now()
	_, _, live, err := m.parseLinux(1, sampleLinux, now)
	if err != nil {
		t.Fatal(err)
	}
	temps, _ := live["temps"].([]map[string]any)
	if len(temps) != 2 {
		t.Fatalf("temps = %v, want 2", temps)
	}
	if temps[0]["label"] != "x86_pkg_temp" || temps[0]["value"] != 45.0 {
		t.Errorf("temp[0] = %v", temps[0])
	}
	if temps[1]["value"] != 38.0 {
		t.Errorf("temp[1] = %v", temps[1])
	}
	procs, _ := live["procs"].([]map[string]any)
	if len(procs) != 2 || procs[0]["name"] != "java" || procs[0]["cpu"] != 12.3 {
		t.Errorf("procs = %v", procs)
	}
	if procs[0]["pid"] != int64(1234) {
		t.Errorf("pid = %v", procs[0]["pid"])
	}
}

func TestParseLinuxProcsAux(t *testing.T) {
	// busybox ps aux 回退格式
	sec := `USER       PID %CPU %MEM    VSZ   RSS TTY      STAT START   TIME COMMAND
root         1  0.0  0.1  16864  3924 ?        Ss   10:00   0:01 /sbin/init
root       200  1.5  2.3 123456 23456 ?        S    10:01   0:05 /usr/bin/dockerd
`
	procs := parseLinuxProcs(sec)
	if len(procs) != 2 {
		t.Fatalf("procs = %v, want 2", procs)
	}
	if procs[0]["pid"] != int64(1) || procs[0]["cpu"] != 0.0 {
		t.Errorf("proc[0] = %v", procs[0])
	}
	if procs[1]["name"] != "/usr/bin/dockerd" || procs[1]["mem"] != 2.3 {
		t.Errorf("proc[1] = %v", procs[1])
	}
}

func TestParseLinuxCPUDelta(t *testing.T) {
	m := newTestManager()
	now := time.Now()
	if _, _, _, err := m.parseLinux(1, sampleLinux, now); err != nil {
		t.Fatal(err)
	}
	// 5 秒后：total 49100→60100(+11000)，idle 47000→55000(+8000) → busy 3000/11000 ≈ 27.27%
	out2 := replaceFirstCpuLine(sampleLinux, "cpu  2500 0 2500 54000 1000 0 100 0 0 0")
	s, _, _, err := m.parseLinux(1, out2, now.Add(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if s.CPU < 26.5 || s.CPU > 28 {
		t.Errorf("cpu delta = %v, want ~27.27", s.CPU)
	}
	// 每核差分同步推进（首轮 per_cpu 值在 out2 再差分应得到有效值）
	_, _, live2, err := m.parseLinux(1, sampleLinux, now.Add(10*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if per, _ := live2["per_cpu"].([]float64); len(per) != 2 {
		t.Errorf("per_cpu = %v", live2["per_cpu"])
	}
}

func replaceFirstCpuLine(out, line string) string {
	return "##CPU##\n" + line + out[len("##CPU##\ncpu  1000 0 1000 46000 1000 0 100 0 0 0"):]
}

func TestParseWindows(t *testing.T) {
	out := "prefix\r\n{\"cpu\":37,\"mem_used\":3221225472,\"mem_total\":8589934592,\"disk_pct\":55.5,\"uptime\":3600,\"hostname\":\"WIN-TEST\",\"per_cpu\":[12.5,44.0],\"procs\":[{\"pid\":100,\"name\":\"svchost\",\"cpu\":8.8,\"mem\":2.1}]}\r\n"
	s, detail, live, err := parseWindows(out, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if s.CPU != 37 || s.MemUsed != 3221225472 || s.DiskPct != 55.5 || s.Uptime != 3600 {
		t.Errorf("sample = %+v", s)
	}
	if detail["hostname"] != "WIN-TEST" {
		t.Errorf("hostname = %v", detail["hostname"])
	}
	if per, _ := live["per_cpu"].([]float64); len(per) != 2 || per[0] != 12.5 {
		t.Errorf("per_cpu = %v", live["per_cpu"])
	}
	if procs, _ := live["procs"].([]map[string]any); len(procs) != 1 || procs[0]["name"] != "svchost" {
		t.Errorf("procs = %v", live["procs"])
	}
}

func TestParseWindowsScalarArray(t *testing.T) {
	// Windows PowerShell 5.1 单元素数组会退化为标量 → 应容错包装
	out := "{\"cpu\":10,\"mem_used\":1,\"mem_total\":2,\"disk_pct\":3,\"uptime\":4,\"hostname\":\"W\",\"per_cpu\":55.5,\"procs\":{\"pid\":7,\"name\":\"x\",\"cpu\":1.0,\"mem\":2.0}}"
	_, _, live, err := parseWindows(out, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if per, _ := live["per_cpu"].([]float64); len(per) != 1 || per[0] != 55.5 {
		t.Errorf("per_cpu 标量未包装: %v", live["per_cpu"])
	}
	if procs, _ := live["procs"].([]map[string]any); len(procs) != 1 || procs[0]["pid"] != int64(7) {
		t.Errorf("procs 标量未包装: %v", live["procs"])
	}
}

func TestSplitSections(t *testing.T) {
	m := splitSections(sampleLinux)
	if m["CPU"] == "" || m["MEM"] == "" || m["DF"] == "" || m["TEMP"] == "" || m["PROC"] == "" {
		t.Errorf("sections = %v", m)
	}
}
