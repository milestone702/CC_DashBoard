// API handler 层测试（httptest 直接驱动路由，覆盖认证/规则/节点/上报/导出）。
package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"cc_dashboard/server/internal/alert"
	"cc_dashboard/server/internal/store"
)

// newTestServer 构建完整路由（空 web 目录 → 无嵌入前端也可用）。
func newTestServer(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := store.Open(filepath.Join(t.TempDir(), "api_test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	key := bytes.Repeat([]byte{0x42}, 32)
	r, _ := NewRouter(db, key, NewHub(), alert.NewEngine(db), "")
	return r
}

// doJSON 发送 JSON 请求，携带可选会话 cookie。
func doJSON(t *testing.T, r *gin.Engine, method, path, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	var err error
	if body != "" {
		req, err = http.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req, err = http.NewRequest(method, path, nil)
	}
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// adminSession 初始化管理员并返回会话 cookie。
func adminSession(t *testing.T, r *gin.Engine) *http.Cookie {
	t.Helper()
	w := doJSON(t, r, http.MethodPost, "/api/v1/auth/setup", `{"password":"secret123"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("setup = %d %s", w.Code, w.Body.String())
	}
	res := w.Result()
	for _, ck := range res.Cookies() {
		if ck.Name == "cc_session" {
			return ck
		}
	}
	t.Fatal("setup 未返回会话 cookie")
	return nil
}

func TestAuthSetupAndLogin(t *testing.T) {
	r := newTestServer(t)

	// 初始状态
	w := doJSON(t, r, http.MethodGet, "/api/v1/auth/status", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"admin_set":false`) {
		t.Fatalf("status = %d %s", w.Code, w.Body.String())
	}

	// setup 两次 → 第二次 400
	ck := adminSession(t, r)
	if w := doJSON(t, r, http.MethodPost, "/api/v1/auth/setup", `{"password":"secret123"}`, ck); w.Code != http.StatusBadRequest {
		t.Errorf("重复 setup = %d, want 400", w.Code)
	}

	// 弱密码
	w = doJSON(t, r, http.MethodPost, "/api/v1/auth/login", `{"password":"123","scope":"admin"}`)
	if w.Code != http.StatusBadRequest && w.Code != http.StatusUnauthorized {
		t.Errorf("弱密码 login = %d", w.Code)
	}

	// 正确密码登录
	w = doJSON(t, r, http.MethodPost, "/api/v1/auth/login", `{"password":"secret123","scope":"admin"}`)
	if w.Code != 200 {
		t.Fatalf("login = %d %s", w.Code, w.Body.String())
	}
	var got bool
	for _, c := range w.Result().Cookies() {
		if c.Name == "cc_session" {
			got = true
		}
	}
	if !got {
		t.Error("login 未返回会话 cookie")
	}
}

func TestRuleCRUD(t *testing.T) {
	r := newTestServer(t)
	ck := adminSession(t, r)

	// 未登录创建 → 401
	if w := doJSON(t, r, http.MethodPost, "/api/v1/alerts/rules",
		`{"node_id":0,"metric":"cpu","op":">","threshold":80}`); w.Code != http.StatusUnauthorized {
		t.Errorf("匿名创建规则 = %d, want 401", w.Code)
	}

	// 创建
	w := doJSON(t, r, http.MethodPost, "/api/v1/alerts/rules",
		`{"node_id":0,"metric":"cpu","op":">","threshold":80,"delay_sec":0,"enabled":true}`, ck)
	if w.Code != 200 {
		t.Fatalf("create = %d %s", w.Code, w.Body.String())
	}
	var created struct {
		Rule struct {
			ID int64 `json:"id"`
		} `json:"rule"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	if created.Rule.ID == 0 {
		t.Fatal("创建规则无 id")
	}

	// 更新（改指标/阈值/延时/启用位）
	w = doJSON(t, r, http.MethodPut, "/api/v1/alerts/rules/"+itoa(created.Rule.ID),
		`{"node_id":0,"metric":"mem_pct","op":">=","threshold":90,"delay_sec":300,"enabled":false}`, ck)
	if w.Code != 200 {
		t.Fatalf("update = %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"mem_pct"`) || !strings.Contains(w.Body.String(), `"enabled":false`) {
		t.Errorf("update 返回未含新值: %s", w.Body.String())
	}

	// 列表回读
	w = doJSON(t, r, http.MethodGet, "/api/v1/alerts/rules", "", ck)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"threshold":90`) {
		t.Errorf("list = %d %s", w.Code, w.Body.String())
	}

	// 非法阈值 → 400
	if w := doJSON(t, r, http.MethodPut, "/api/v1/alerts/rules/"+itoa(created.Rule.ID),
		`{"metric":"cpu","threshold":200}`, ck); w.Code != http.StatusBadRequest {
		t.Errorf("非法阈值 = %d, want 400", w.Code)
	}

	// 删除 → 200；再删 → 404；更新已删 → 404
	if w := doJSON(t, r, http.MethodDelete, "/api/v1/alerts/rules/"+itoa(created.Rule.ID), "", ck); w.Code != 200 {
		t.Errorf("delete = %d", w.Code)
	}
	if w := doJSON(t, r, http.MethodDelete, "/api/v1/alerts/rules/"+itoa(created.Rule.ID), "", ck); w.Code != http.StatusNotFound {
		t.Errorf("重复 delete = %d, want 404", w.Code)
	}
	if w := doJSON(t, r, http.MethodPut, "/api/v1/alerts/rules/"+itoa(created.Rule.ID),
		`{"metric":"cpu","threshold":80}`, ck); w.Code != http.StatusNotFound {
		t.Errorf("更新已删规则 = %d, want 404", w.Code)
	}
}

func TestNodeRotateTokenAndHostKey(t *testing.T) {
	r := newTestServer(t)
	ck := adminSession(t, r)

	// 创建 agent 节点
	w := doJSON(t, r, http.MethodPost, "/api/v1/nodes",
		`{"name":"n1","os_type":"linux","access_mode":"agent"}`, ck)
	if w.Code != 200 {
		t.Fatalf("create node = %d %s", w.Code, w.Body.String())
	}
	var created struct {
		Node struct {
			ID    int64  `json:"id"`
			Token string `json:"token"`
		} `json:"node"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	oldToken := created.Node.Token
	if oldToken == "" {
		t.Fatal("创建节点无 token")
	}

	// 轮换 Token
	w = doJSON(t, r, http.MethodPut, "/api/v1/nodes/"+itoa(created.Node.ID),
		`{"rotate_token":true}`, ck)
	if w.Code != 200 {
		t.Fatalf("rotate_token = %d %s", w.Code, w.Body.String())
	}
	var rotated struct {
		Node struct {
			Token string `json:"token"`
		} `json:"node"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &rotated)
	if rotated.Node.Token == "" || rotated.Node.Token == oldToken {
		t.Errorf("轮换后 token 未变化: %s", rotated.Node.Token)
	}

	// 重置主机指纹（无指纹时应幂等成功）
	w = doJSON(t, r, http.MethodPut, "/api/v1/nodes/"+itoa(created.Node.ID),
		`{"rotate_host_key":true}`, ck)
	if w.Code != 200 {
		t.Errorf("rotate_host_key = %d %s", w.Code, w.Body.String())
	}

	// 未登录 → 401
	if w := doJSON(t, r, http.MethodPut, "/api/v1/nodes/"+itoa(created.Node.ID),
		`{"rotate_token":true}`); w.Code != http.StatusUnauthorized {
		t.Errorf("匿名轮换 = %d, want 401", w.Code)
	}
}

func TestReportSummaryAndMetrics(t *testing.T) {
	r := newTestServer(t)
	ck := adminSession(t, r)

	// 创建节点拿 token
	w := doJSON(t, r, http.MethodPost, "/api/v1/nodes",
		`{"name":"agent1","os_type":"linux","access_mode":"agent","report_interval":5}`, ck)
	if w.Code != 200 {
		t.Fatalf("create node = %d", w.Code)
	}
	var created struct {
		Node struct {
			ID    int64  `json:"id"`
			Token string `json:"token"`
		} `json:"node"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)

	// 无 token 上报 → 401
	if w := doJSON(t, r, http.MethodPost, "/api/v1/report", `{"cpu":1}`); w.Code != http.StatusUnauthorized {
		t.Errorf("无 token report = %d, want 401", w.Code)
	}

	// 正常上报（新字段一并验证落库）
	body := `{"cpu":42.5,"mem_used":1073741824,"mem_total":2147483648,"disk_pct":55,
		"net_rx":1000,"net_tx":500,"load1":0.5,"uptime":100,
		"disk_read":12345,"disk_write":54321,"per_cpu":[10.5,20.5],
		"live":{"cpu_total":42.5,"per_cpu":[10.5,20.5]}}`
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/report", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CC-Node-Token", created.Node.Token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("report = %d %s", w.Code, w.Body.String())
	}

	// summary 反映在线
	w = doJSON(t, r, http.MethodGet, "/api/v1/summary", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"online":1`) {
		t.Errorf("summary = %d %s", w.Code, w.Body.String())
	}

	// 节点列表含最新样本
	w = doJSON(t, r, http.MethodGet, "/api/v1/nodes", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"cpu":42.5`) {
		t.Errorf("nodes = %d %s", w.Code, w.Body.String())
	}

	// Prometheus /metrics
	w = doJSON(t, r, http.MethodGet, "/metrics", "")
	if w.Code != 200 {
		t.Fatalf("/metrics = %d", w.Code)
	}
	mw := w.Body.String()
	for _, want := range []string{
		"cc_node_up{id=\"1\",name=\"agent1\"} 1",
		"cc_node_cpu_percent", "cc_node_disk_read_bytes_per_second", "cc_scrape_nodes_total",
	} {
		if !strings.Contains(mw, want) {
			t.Errorf("/metrics 缺少 %q\n%s", want, mw)
		}
	}

	// 历史 CSV 导出
	w = doJSON(t, r, http.MethodGet,
		"/api/v1/nodes/"+itoa(created.Node.ID)+"/history?range=1h&format=csv", "")
	if w.Code != 200 {
		t.Fatalf("csv = %d %s", w.Code, w.Body.String())
	}
	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/csv") {
		t.Errorf("csv content-type = %s", ct)
	}
	csvBody := w.Body.String()
	if !strings.Contains(csvBody, "ts,datetime,cpu_pct") || !strings.Contains(csvBody, "12345") {
		t.Errorf("csv 内容异常:\n%s", csvBody)
	}

	// 历史 JSON（默认）含 字段
	w = doJSON(t, r, http.MethodGet,
		"/api/v1/nodes/"+itoa(created.Node.ID)+"/history?range=1h", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"disk_read":12345`) {
		t.Errorf("history json = %d %s", w.Code, w.Body.String())
	}
}

func itoa(v int64) string {
	return strconv.FormatInt(v, 10)
}

// TestCSRFGuard T-12：Origin 同源校验 + SameSite=Lax Cookie。
func TestCSRFGuard(t *testing.T) {
	r := newTestServer(t)
	ck := adminSession(t, r)

	// 1) 会话 Cookie 带 SameSite=Lax
	w := doJSON(t, r, http.MethodPost, "/api/v1/auth/login", `{"password":"secret123","scope":"admin"}`)
	setCookie := w.Header().Get("Set-Cookie")
	if !strings.Contains(strings.ToLower(setCookie), "samesite=lax") {
		t.Errorf("Set-Cookie 缺 SameSite=Lax: %s", setCookie)
	}

	// 2) 同源 Origin → 放行（创建规则成功）
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/alerts/rules",
		strings.NewReader(`{"node_id":0,"metric":"cpu","op":">","threshold":80}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://example.com")
	req.Host = "example.com"
	req.AddCookie(ck)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("同源 Origin = %d %s", w.Code, w.Body.String())
	}

	// 3) 跨站 Origin → 403
	req2, _ := http.NewRequest(http.MethodPost, "/api/v1/alerts/rules",
		strings.NewReader(`{"node_id":0,"metric":"cpu","op":">","threshold":80}`))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Origin", "http://evil.example")
	req2.Host = "example.com"
	req2.AddCookie(ck)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusForbidden {
		t.Errorf("跨站 Origin = %d, want 403", w2.Code)
	}

	// 4) 无 Origin（Agent 上报 / curl）→ 放行
	if w := doJSON(t, r, http.MethodPost, "/api/v1/report", `{"cpu":1}`); w.Code == http.StatusForbidden {
		t.Errorf("无 Origin 不应 403（got %d）", w.Code)
	}

	// 5) X-Forwarded-Host 反代场景：Origin 与转发头一致 → 放行
	req3, _ := http.NewRequest(http.MethodPost, "/api/v1/alerts/rules",
		strings.NewReader(`{"node_id":0,"metric":"cpu","op":">","threshold":80}`))
	req3.Header.Set("Content-Type", "application/json")
	req3.Header.Set("Origin", "https://dash.example.com")
	req3.Header.Set("X-Forwarded-Host", "dash.example.com")
	req3.Host = "127.0.0.1:8080" // upstream 地址
	req3.AddCookie(ck)
	w3 := httptest.NewRecorder()
	r.ServeHTTP(w3, req3)
	if w3.Code != http.StatusOK {
		t.Errorf("反代 Origin = %d %s", w3.Code, w3.Body.String())
	}
}
