package collector

import (
	"strings"
	"testing"
	"time"
)

func testESXIMgr() *Manager {
	return &Manager{
		prevCPU:       map[int64]cpuSample{},
		prevNet:       map[int64]netSample{},
		prevESXI:      map[int64]esxiNetSample{},
		prevESXINic:   map[int64]map[string]esxiNetSample{},
		prevESXINicTS: map[int64]time.Time{},
	}
}

const sampleClassicEsxtop = `##HOST##
esxi-host01
##UP##
93784 seconds
##CPU##
16
##MEM##
   Physical Memory: 68719476736 bytes
##FS##
Mount Point                                        Volume Name    UUID                                 Mounted  Type    Size          Free
-------------------------------------------------  -------------  -----------------------------------  -------  ------  ------------  -----------
/vmfs/volumes/abc-123                              datastore1     abc-123                              true     VMFS-6  1006901395456 500000000000
##ESXTOP##
CPU: 10:00:01 up 100 days,  load average: 2.00, 1.90, 1.80
 worldID GROUP uswSysType %USED %RUNTIME %WAIT %IDLE
      0 00                0.0   0.0      0.0  100.0
 100001 1                 45.5  46.0     1.0  53.0
 100002 1                 32.5  33.0     0.5  66.0

MEM: 10:00:01
 worldID GROUP MB MEMCTL %SWAP
      0 00    0  0.0    0.0
 100001 1     2048 100.0 0.0
 100002 1     1024 50.0  0.0
`

func TestParseESXIClassic(t *testing.T) {
	m := testESXIMgr()
	s, detail, live, err := m.parseESXI(1, sampleClassicEsxtop, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// classic cpu = 78/16 = 4.875
	if s.CPU < 4.8 || s.CPU > 4.95 {
		t.Errorf("cpu = %v, want ~4.88", s.CPU)
	}
	if s.Uptime != 93784 {
		t.Errorf("uptime = %d", s.Uptime)
	}
	if s.DiskPct < 50 || s.DiskPct > 51 {
		t.Errorf("disk_pct = %v, want ~50 (datastore1)", s.DiskPct)
	}
	if mounts, _ := detail["mounts"].([]map[string]any); len(mounts) != 1 {
		t.Errorf("mounts = %v", detail["mounts"])
	}
	if live == nil || live["cpu_total"] == nil {
		t.Errorf("live = %v", live)
	}
}

// 真机风格 PDH-CSV 摘录（含反斜杠转义 \% 与 Util/Processor 双计数器，单位为百分比；
// Cpu Load 为 0~1 比例）。
const sampleCSVEsxtop = `"(PDH-CSV 4.0) (UTC)(0)","\\esxi\Physical Cpu Load\Cpu Load (1 Minute Avg)","\\esxi\Physical Cpu(0)\% Util Time","\\esxi\Physical Cpu(1)\% Util Time","\\esxi\Physical Cpu(_Total)\% Util Time","\\esxi\Physical Cpu(_Total)\% Processor Time","\\esxi\Numa Node(0)\% Processor Time","\\esxi\Memory\Machine MBytes","\\esxi\Memory\Free MBytes"
"10/10/2026 05:00:00.000","0.10","10.48","7.40","8.94","14.34","14.34","32512.00","16384.00"
`

func TestParseEsxtopCSV(t *testing.T) {
	cpu, memUsed, load1, ncpu, perCPU := parseEsxtopCSV(sampleCSVEsxtop, 34113597440)
	// 优先 Util Time：真值 8.94 ≈ ESXi 界面 9%
	if cpu < 8.9 || cpu > 9.0 {
		t.Errorf("cpu = %v, want ~8.94 (Util Time)", cpu)
	}
	if ncpu != 2 {
		t.Errorf("ncpu = %d, want 2", ncpu)
	}
	if memUsed != 16128*1024*1024 {
		t.Errorf("mem_used = %d", memUsed)
	}
	// load 比例 0.10 × 2核 = 0.20
	if load1 < 0.19 || load1 > 0.21 {
		t.Errorf("load1 = %v, want ~0.20", load1)
	}
	// 每核优先 Util：[10.48, 7.40]
	if len(perCPU) != 2 || perCPU[0] < 10.4 || perCPU[1] < 7.3 {
		t.Errorf("per_cpu = %v, want ~[10.48 7.40] (Util)", perCPU)
	}
}

// 无 Util 计数器的旧版本：回退 Processor Time。
func TestParseEsxtopCSVProcessorFallback(t *testing.T) {
	fixture := `"(PDH-CSV 4.0)","\\h\Physical Cpu(0)% Processor Time","\\h\Physical Cpu(_Total)% Processor Time"
"ts","25.21","14.34"
`
	cpu, _, _, ncpu, perCPU := parseEsxtopCSV(fixture, 0)
	if cpu != 14.34 {
		t.Errorf("cpu = %v, want 14.34 (Processor fallback)", cpu)
	}
	if ncpu != 1 || perCPU[0] != 25.21 {
		t.Errorf("ncpu=%d per=%v", ncpu, perCPU)
	}
}

func TestNormalizeUptime(t *testing.T) {
	if got := normalizeUptime(2086842365871); got < 2086000 || got > 2088000 {
		t.Errorf("µs = %d, want ~2086842", got)
	}
	if got := normalizeUptime(93784); got != 93784 {
		t.Errorf("seconds = %d", got)
	}
}

func TestParseFS(t *testing.T) {
	fs := `Mount Point                                        Volume Name                                 UUID                                 Mounted  Type             Size          Free
-------------------------------------------------  ------------------------------------------  -----------------------------------  -------  ------  -------------  ------------
/vmfs/volumes/66401366-x  datastore1  66401366-x  true  VMFS-6  1006901395456  367277375488
/vmfs/volumes/02ab7ede-y  BOOTBANK1   02ab7ede-y  true  vfat    4293591040     3997106176
`
	mounts, pct := parseFS(fs)
	if len(mounts) != 2 {
		t.Fatalf("mounts = %v", mounts)
	}
	// datastore1 used = 1006901395456-367277375488 → 63.5%
	if pct < 63 || pct > 64 {
		t.Errorf("disk_pct = %v, want ~63.5", pct)
	}
	if mounts[0]["device"] != "datastore1" || mounts[0]["fstype"] != "VMFS-6" {
		t.Errorf("mount0 = %v", mounts[0])
	}
}

func TestParseNICTX(t *testing.T) {
	out := `--vmnic0--
NIC statistics for vmnic0
   Bytes received: 48951888708
   Bytes sent: 36633284415
--vmnic1--
NIC statistics for vmnic1
   Bytes received: 1000
   Bytes sent: 2000
`
	m := parseNICTX(out)
	if len(m) != 2 {
		t.Fatalf("nics = %v", m)
	}
	if m["vmnic0"].rx != 48951888708 || m["vmnic0"].tx != 36633284415 {
		t.Errorf("vmnic0 = %+v", m["vmnic0"])
	}
	if m["vmnic1"].rx != 1000 {
		t.Errorf("vmnic1 = %+v", m["vmnic1"])
	}
}

func TestParseNICListAndIP(t *testing.T) {
	nics := parseNICList(`Name    PCI Device    Driver    Admin Status  Link Status  Speed  Duplex  MAC Address         MTU  Description
------  ------------  --------  ------------  -----------  -----  ------  -----------------  ----  -----------
vmnic0  0000:05:00.0  cndi_igc  Up            Up            2500  Full    00:d0:4c:10:38:f0  1500  Intel NIC
`)
	if len(nics) != 1 || nics[0]["mac"] != "00:d0:4c:10:38:f0" || nics[0]["speed"] != "2500" {
		t.Errorf("nics = %v", nics)
	}
	ips := parseIP4(`Name  IPv4 Address   IPv4 Netmask
----  -------------  --------------
vmk0  192.168.6.200  255.255.255.0
`)
	if ips["vmk0"] != "192.168.6.200" {
		t.Errorf("ips = %v", ips)
	}
	macs := parseIfaceMAC(`vmk0
   Name: vmk0
   MAC Address: 00:d0:4c:10:38:f0
   Enabled: true
`)
	if macs["vmk0"] != "00:d0:4c:10:38:f0" {
		t.Errorf("macs = %v", macs)
	}
}

func TestParsePhysicalMemUnits(t *testing.T) {
	if v := parsePhysicalMem("Physical Memory: 2 GB"); v != 2*1024*1024*1024 {
		t.Errorf("gb: %d", v)
	}
	if v := parsePhysicalMem("Physical Memory: 34113597440 Bytes"); v != 34113597440 {
		t.Errorf("bytes: %d", v)
	}
}

func TestFirstIntAndSplit(t *testing.T) {
	if firstInt("  93784 seconds") != 93784 {
		t.Error("firstInt")
	}
	m := splitSections("##A##\nhello\n##B##\nworld\n")
	if strings.TrimSpace(m["A"]) != "hello" || strings.TrimSpace(m["B"]) != "world" {
		t.Errorf("split = %v", m)
	}
}

func TestParseESXITemps(t *testing.T) {
	// 真机风格 esxcli 输出（空格对齐表 + 表头）
	sec := `Key                                     Value      Unit    Warning  Critical  Alarm     Entity
CPU1 temSENSOR                          48         C       na       na        na        cpu0
CPU2 temSENSOR                          51         C       na       na        na        cpu1
System Control                          3.3        V       na       na        na        mainboard
`
	temps := parseESXITemps(sec)
	if len(temps) != 2 {
		t.Fatalf("temps = %v, want 2 个温度传感器（电压行应被过滤）", temps)
	}
	if temps[0]["label"] != "CPU1 temSENSOR" || temps[0]["value"] != 48.0 {
		t.Errorf("temps[0] = %v", temps[0])
	}
	if temps[1]["value"] != 51.0 {
		t.Errorf("temps[1] = %v", temps[1])
	}
	// 空输入 / 无传感器
	if got := parseESXITemps(""); len(got) != 0 {
		t.Errorf("empty = %v", got)
	}
	if got := parseESXITemps("No data available"); len(got) != 0 {
		t.Errorf("no data = %v", got)
	}
}

func TestParseESXIDiskIOAndRates(t *testing.T) {
	// esxcli storage core device stats get 输出（设备行无缩进，计数行缩进）
	sec := `naa.5000cca25d2b4234
   Read: 10000
   Write: 20000
   Abort Command: 0
mpx.vmhba0:C0:T0:L0
   Read: 500
   Write: 600
`
	cur := parseESXIDiskIO(sec)
	if len(cur) != 2 {
		t.Fatalf("devices = %v, want 2", cur)
	}
	if cur["naa.5000cca25d2b4234"].read != 10000 || cur["naa.5000cca25d2b4234"].write != 20000 {
		t.Errorf("naa stats = %+v", cur["naa.5000cca25d2b4234"])
	}
	m := testESXIMgr()
	now := time.Now()
	// 首轮建立基线：无速率
	if _, r, w := m.esxiDiskRates(1, cur, now); r != 0 || w != 0 {
		t.Errorf("首轮应为0: r=%d w=%d", r, w)
	}
	// 5 秒后 +1500读/+3000写 → 300/600 IOPS
	cur2 := map[string]esxiDiskSample{
		"naa.5000cca25d2b4234": {read: 11500, write: 23000},
		"mpx.vmhba0:C0:T0:L0":  {read: 510, write: 620},
	}
	rates, r, w := m.esxiDiskRates(1, cur2, now.Add(5*time.Second))
	if r < 302 || r > 304 || w < 600 || w > 604 {
		t.Errorf("iops r=%d w=%d, want ~302/602", r, w)
	}
	if rates["naa.5000cca25d2b4234"][0] < 300 {
		t.Errorf("per-device = %v", rates["naa.5000cca25d2b4234"])
	}
}

func TestParseDiskWaitMS(t *testing.T) {
	csv := `"(PDH-CSV 4.0)","\\h\Disk(naa.x)\Average Wait Time","\\h\Physical Cpu(_Total)% Processor Time"
"ts","2.35","8.94"
`
	if got := parseDiskWaitMS(csv); got != 2.35 {
		t.Errorf("lat = %v, want 2.35", got)
	}
	// 无磁盘等待列 / 非 CSV
	if got := parseDiskWaitMS("CPU: 10:00:01"); got != 0 {
		t.Errorf("classic = %v, want 0", got)
	}
}
