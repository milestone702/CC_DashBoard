package collector

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// dsm7UtilJSON DSM 7.2 真机口径摘录（2026-10-10 实测字段）。
const dsm7UtilJSON = `{"success":true,"data":{
  "cpu":{"15min_load":6,"1min_load":16,"5min_load":10,"device":"System","other_load":2,"system_load":0,"user_load":1},
  "memory":{"avail_real":198604,"avail_swap":6447764,"buffer":702060,"cached":5455472,"device":"Memory",
            "memory_size":8388608,"real_usage":21,"si_disk":0,"so_disk":0,"swap_usage":7,
            "total_real":8135148,"total_swap":6977528},
  "network":[{"device":"total","rx":9936,"tx":2298},{"device":"eth0","rx":3901,"tx":2202},
             {"device":"eth1","rx":6035,"tx":96},{"device":"zt0","rx":0,"tx":0}],
  "disk":{"disk":[{"device":"nvme0n1","read_byte":1024,"write_byte":17920,"read_access":0,"write_access":2,"utilization":0}],
          "total":{"device":"total","read_byte":30720,"write_byte":160000,"read_access":0,"write_access":14,"utilization":0}},
  "space":{"total":{"device":"total","read_byte":0,"write_byte":38912,"utilization":1},
           "volume":[{"device":"md2","display_name":"volume1","utilization":1}]}
}}`

// dsm7InfoJSON SYNO.Core.System method=info 真机摘录。
const dsm7InfoJSON = `{"success":true,"data":{
  "up_time":"584:47:0","sys_temp":0,"model":"DS3617xs",
  "cpu_family":"Core(TM)","cpu_series":"i3-N305","cpu_cores":"4C/4T",
  "cpu_clock_speed":1804,"ram_size":8192,"firmware_ver":"RR v25.9.4 / DSM 7.2.2-72806 Update 9"
}}`

func TestSynoDSM7Sample(t *testing.T) {
	var u synoUtilization
	if err := json.Unmarshal([]byte(dsm7UtilJSON), &u); err != nil {
		t.Fatal(err)
	}
	var info synoSystemInfo
	if err := json.Unmarshal([]byte(dsm7InfoJSON), &info); err != nil {
		t.Fatal(err)
	}
	m := &Manager{}
	now := time.Now()
	s := m.synoSample(&u, &info, now)

	// CPU：user1+system0+other2 = 3
	if s.CPU < 2.9 || s.CPU > 3.1 {
		t.Errorf("cpu = %v, want 3（user+system+other）", s.CPU)
	}
	if s.Load1 != 16 {
		t.Errorf("load1 = %v, want 16", s.Load1)
	}
	// 内存：KB→字节；used = total-avail-buffer-cached
	wantTotal := int64(8135148 * 1024)
	wantUsed := int64((8135148 - 198604 - 702060 - 5455472) * 1024)
	if s.MemTotal != wantTotal {
		t.Errorf("mem_total = %d, want %d", s.MemTotal, wantTotal)
	}
	if s.MemUsed != wantUsed {
		t.Errorf("mem_used = %d, want %d", s.MemUsed, wantUsed)
	}
	// 网络：瞬时速率直接采用（真机实测非累计），取 device=total
	if s.NetRx != 9936 || s.NetTx != 2298 {
		t.Errorf("net = %d/%d, want 9936/2298（速率直采）", s.NetRx, s.NetTx)
	}
	// 磁盘 I/O 速率 + 存储占用
	if s.DiskRead != 30720 || s.DiskWrite != 160000 {
		t.Errorf("disk io = %d/%d, want 30720/160000", s.DiskRead, s.DiskWrite)
	}
	if s.DiskPct != 1 {
		t.Errorf("disk_pct = %v, want 1（space.utilization）", s.DiskPct)
	}
	// uptime: "584:47:0" → 584*3600+47*60 = 2105220
	if s.Uptime != 2105220 {
		t.Errorf("uptime = %d, want 2105220", s.Uptime)
	}
}

func TestSynoDSM7Live(t *testing.T) {
	var u synoUtilization
	if err := json.Unmarshal([]byte(dsm7UtilJSON), &u); err != nil {
		t.Fatal(err)
	}
	var info synoSystemInfo
	if err := json.Unmarshal([]byte(dsm7InfoJSON), &info); err != nil {
		t.Fatal(err)
	}
	m := &Manager{}
	s := m.synoSample(&u, &info, time.Now())
	procs := []synoProcess{
		{PID: 1, Command: "busy", CPU: 125, Mem: 512000, MemShared: 1024},
		{PID: 2, Command: "idle", CPU: 0, Mem: 4096, MemShared: 2048},
	}
	live := synoLive(s, &u, &info, procs, time.Now())

	// 分网卡：排除 total 与非物理虚拟口？zt 口保留（真实接口），排除 total/lo
	nics, _ := live["nics"].([]map[string]any)
	if len(nics) != 3 {
		t.Fatalf("nics = %v, want 3（排除 total）", nics)
	}
	if nics[0]["rx_bps"] != int64(3901) {
		t.Errorf("eth0 rx = %v", nics[0]["rx_bps"])
	}
	// 磁盘 I/O 进 live
	dio, _ := live["disk_io"].(map[string]any)
	if dio["read_bps"] != int64(30720) || dio["write_iops"] != int64(14) {
		t.Errorf("disk_io = %v", dio)
	}
	// 温度：sys_temp=0（虚拟机无传感器）→ 空数组
	if temps, _ := live["temps"].([]map[string]any); len(temps) != 0 {
		t.Errorf("temps = %v, want 空", temps)
	}
	// 进程 Top5：cpu 0.1% 粒度换算、mem KB→%
	liveProcs, _ := live["procs"].([]map[string]any)
	if len(liveProcs) != 2 {
		t.Fatalf("procs = %v", liveProcs)
	}
	if liveProcs[0]["name"] != "busy" || liveProcs[0]["cpu"] != 12.5 {
		t.Errorf("proc[0] = %v, want busy 12.5%%", liveProcs[0])
	}
	// mem% = 512000KB*1024 / (8135148-198604-702060-5455472)*1024 *100 ≈ 28.8%
	if m, _ := liveProcs[0]["mem"].(float64); m < 6.2 || m > 6.4 {
		t.Errorf("proc mem%% = %v, want ~6.29", liveProcs[0]["mem"])
	}
	// 每核/进程：DSM 无 API → 空
	if per, _ := live["per_cpu"].([]float64); len(per) != 0 {
		t.Errorf("per_cpu = %v", per)
	}
	// mem 细分
	mem, _ := live["mem"].(map[string]any)
	if mem["percent"] != 21.0 {
		t.Errorf("mem percent = %v, want 21（real_usage）", mem["percent"])
	}
	if mem["swap_total"] != int64(6977528*1024) {
		t.Errorf("swap_total = %v", mem["swap_total"])
	}
}

func TestSynoDSM6Compat(t *testing.T) {
	// DSM6 旧字段：cpu user/system/idle + memory total/used（MB）+ 网络累计（此时按速率直采口径不适用，
	// 仅验证 CPU/内存兼容分支）
	u := &synoUtilization{}
	u.Success = true
	u.Data.CPU.User = 5.5
	u.Data.CPU.System = 2.5
	u.Data.CPU.Idle = 92
	u.Data.Memory.Total = 16384  // MB 尺度
	u.Data.Memory.Used = 8192
	m := &Manager{}
	s := m.synoSample(u, nil, time.Now())
	if s.CPU < 7.9 || s.CPU > 8.1 {
		t.Errorf("dsm6 cpu = %v, want 8", s.CPU)
	}
	if s.MemTotal != 16384*1024*1024 || s.MemUsed != 8192*1024*1024 {
		t.Errorf("dsm6 mem total=%d used=%d", s.MemTotal, s.MemUsed)
	}
	// 64GB 字节尺度不再放大
	u.Data.Memory.Total = 68719476736
	u.Data.Memory.Used = 34359738368
	s2 := m.synoSample(u, nil, time.Now())
	if s2.MemTotal != 68719476736 {
		t.Errorf("bytes scale mem_total = %d", s2.MemTotal)
	}
	// idle-only 回退：100-idle
	u.Data.CPU.User, u.Data.CPU.System, u.Data.CPU.Idle = 0, 0, 50
	s3 := m.synoSample(u, nil, time.Now())
	if s3.CPU != 50 {
		t.Errorf("cpu from idle = %v", s3.CPU)
	}
}

func TestParseSynoUpTime(t *testing.T) {
	if v := parseSynoUpTime("584:47:0"); v != 2105220 {
		t.Errorf("584:47:0 = %d", v)
	}
	if v := parseSynoUpTime("3600"); v != 3600 {
		t.Errorf("plain = %d", v)
	}
	if v := parseSynoUpTime(""); v != 0 {
		t.Errorf("empty = %d", v)
	}
	if v := parseSynoUpTime("bad:format"); v != 0 {
		t.Errorf("bad = %d", v)
	}
}

func TestSynoLoginAndFetch(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/webapi/auth.cgi", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("account") == "admin" && r.URL.Query().Get("passwd") == "secret" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data":    map[string]string{"sid": "SID123"},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "code": 403})
	})
	mux.HandleFunc("/webapi/entry.cgi", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("_sid") != "SID123" {
			_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "code": 105})
			return
		}
		switch r.URL.Query().Get("api") {
		case "SYNO.Core.System.Utilization":
			_, _ = w.Write([]byte(dsm7UtilJSON))
		case "SYNO.Core.System":
			// method=info 才返回数据（getinfo 已下线）
			if r.URL.Query().Get("method") != "info" {
				_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "code": 103})
				return
			}
			_, _ = w.Write([]byte(dsm7InfoJSON))
		case "SYNO.Core.Network":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data":    map[string]string{"server_name": "nas-home"},
			})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "code": 102})
		}
	})
	srv := httptest.NewTLSServer(mux)
	defer srv.Close()

	// 登录
	sid, err := synoLogin(srv.Client(), srv.URL, "admin", "secret")
	if err != nil || sid != "SID123" {
		t.Fatalf("login sid=%q err=%v", sid, err)
	}
	if _, err := synoLogin(srv.Client(), srv.URL, "admin", "bad"); err == nil {
		t.Fatal("错误密码应失败")
	}

	// 拉取
	util, err := fetchSynoUtilization(srv.Client(), srv.URL, "SID123")
	if err != nil {
		t.Fatal(err)
	}
	info, err := fetchSynoSystem(srv.Client(), srv.URL, "SID123")
	if err != nil {
		t.Fatal(err)
	}
	if info.Data.Uptime != "584:47:0" {
		t.Errorf("info uptime = %q", info.Data.Uptime)
	}

	// 主机名缓存
	m := &Manager{synoNames: map[int64]string{}}
	if name := m.synoServerName(1, srv.URL, "SID123"); name != "nas-home" {
		t.Errorf("server_name = %q", name)
	}
	if name := m.synoServerName(1, srv.URL, "BADSID"); name != "nas-home" {
		t.Errorf("缓存命中失败: %q", name)
	}

	// 全链路采样
	m2 := &Manager{}
	s := m2.synoSample(util, info, time.Now())
	if s.CPU <= 0 {
		t.Errorf("cpu = %v", s.CPU)
	}
}

func TestSynoErrorEnvelope(t *testing.T) {
	var wrap synoUtilization
	if err := json.Unmarshal([]byte(`{"success":false,"code":105}`), &wrap); err != nil {
		t.Fatal(err)
	}
	if wrap.Success || wrap.Code != 105 {
		t.Errorf("wrap = %+v", wrap)
	}
}

func TestSynoStoragePctAndMounts(t *testing.T) {
	// load_info 容量口径真机摘录（2.35TB/6.08TB ≈ 38.6%）
	raw := `{"success":true,"data":{"volumes":[
	  {"id":"volume_1","fs_type":"ext4","vol_path":"/volume1","status":"normal",
	   "size":{"total":"6081324654592","used":"2346637783040"}}
	]}}`
	var st synoStorage
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		t.Fatal(err)
	}
	pct, ok := st.pct()
	if !ok || pct < 38.5 || pct > 38.7 {
		t.Errorf("pct = %v ok=%v, want ~38.6", pct, ok)
	}
	// 空卷 → false
	empty := &synoStorage{}
	if _, ok := empty.pct(); ok {
		t.Error("空卷应返回 false")
	}
	// load_info 拉取（httptest）
	mux := http.NewServeMux()
	mux.HandleFunc("/webapi/entry.cgi", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(raw))
	})
	srv := httptest.NewTLSServer(mux)
	defer srv.Close()
	got, err := fetchSynoStorage(srv.Client(), srv.URL, "S")
	if err != nil || len(got.Data.Volumes) != 1 || got.Data.Volumes[0].VolPath != "/volume1" {
		t.Fatalf("fetch storage: %v %+v", err, got)
	}
}

func TestSynoProcessList(t *testing.T) {
	// 真机摘录（cpu=0.1% 粒度，mem=KB）
	raw := `{"success":true,"data":{"process":[
	  {"pid":1,"command":"low","cpu":0,"mem":4096,"mem_shared":100,"status":"S"},
	  {"pid":26148,"command":"zerotier-one","cpu":12,"mem":4236,"mem_shared":1164,"status":"R"},
	  {"pid":16934,"command":"java","cpu":0,"mem":199412,"mem_shared":5416,"status":"S"}
	]}}`
	mux := http.NewServeMux()
	mux.HandleFunc("/webapi/entry.cgi", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("api") != "SYNO.Core.System.Process" {
			w.WriteHeader(400)
			return
		}
		_, _ = w.Write([]byte(raw))
	})
	srv := httptest.NewTLSServer(mux)
	defer srv.Close()

	list, err := fetchSynoProcesses(srv.Client(), srv.URL, "S")
	if err != nil {
		t.Fatal(err)
	}
	// 按 cpu 排序：zerotier(12) > java(mem大) > low
	if len(list) != 3 || list[0].Command != "zerotier-one" {
		t.Fatalf("sorted list = %+v", list)
	}
	if list[0].CPU != 12 {
		t.Errorf("cpu = %v", list[0].CPU)
	}
}
