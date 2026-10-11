package collector

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestFetchPVE 用本地 TLS 模拟 PVE API（自签证书，与 InsecureSkipVerify 匹配）。
func TestFetchPVE(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api2/json/nodes/pve1/status", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "PVEAPIToken=root@pam!cc=") {
			w.WriteHeader(401)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"cpu": 0.25, "mem": 2147483648, "maxmem": 8589934592,
				"uptime": 3600, "netin": 1000000, "netout": 500000,
				"disk": 107374182400, "maxdisk": 214748364800,
			},
		})
	})
	mux.HandleFunc("/api2/json/cluster/resources", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"vmid": 101, "name": "web", "status": "running", "type": "qemu",
					"cpu": 0.05, "mem": 536870912, "maxmem": 2147483648},
				{"vmid": 202, "name": "db", "status": "stopped", "type": "lxc",
					"cpu": 0, "mem": 0, "maxmem": 1073741824},
			},
		})
	})
	srv := httptest.NewTLSServer(mux)
	defer srv.Close()

	st, vms, err := fetchPVE(srv.Client(), srv.URL, "PVEAPIToken=root@pam!cc=secret", "pve1")
	if err != nil {
		t.Fatal(err)
	}
	if st.CPU != 0.25 || st.Mem != 2147483648 || st.Uptime != 3600 {
		t.Errorf("status = %+v", st)
	}
	if len(vms) != 2 || vms[0]["name"] != "web" || vms[1]["status"] != "stopped" {
		t.Errorf("vms = %v", vms)
	}

	// 鉴权失败路径
	_, _, err = fetchPVE(srv.Client(), srv.URL, "PVEAPIToken=bad=", "pve1")
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("bad token err = %v", err)
	}
}

// TestPveSampleNetDelta 网络速率差分。
func TestPveSampleNetDelta(t *testing.T) {
	m := &Manager{prevPVE: map[int64]pveNetSample{}}
	t1 := time.Now()
	s1 := m.pveSample(1, &pveStatus{CPU: 0.5, NetIn: 1000, NetOut: 500}, t1)
	if s1.NetRx != 0 {
		t.Errorf("首轮速率应为0: %d", s1.NetRx)
	}
	if s1.CPU != 50 {
		t.Errorf("cpu fraction → 50%%, got %v", s1.CPU)
	}
	t2 := t1.Add(10 * time.Second)
	s2 := m.pveSample(1, &pveStatus{CPU: 0.5, NetIn: 11000, NetOut: 5500}, t2)
	if s2.NetRx != 1000 || s2.NetTx != 500 {
		t.Errorf("rates = rx:%d tx:%d, want 1000/500", s2.NetRx, s2.NetTx)
	}
}
