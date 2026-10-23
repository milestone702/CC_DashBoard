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
	// latest.node_id 必须回填为真实节点 ID（历史缺陷：恒为 0）
	var listed struct {
		Nodes []struct {
			ID     int64 `json:"id"`
			Latest *struct {
				NodeID int64 `json:"node_id"`
			} `json:"latest"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listed); err != nil {
		t.Fatalf("nodes 解析失败: %v", err)
	}
	if len(listed.Nodes) != 1 || listed.Nodes[0].Latest == nil ||
		listed.Nodes[0].Latest.NodeID != listed.Nodes[0].ID {
		t.Errorf("latest.node_id 未回填: %s", w.Body.String())
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

// doReq 发送带自定义请求头的请求（Bearer / WS 等场景）。
func doReq(t *testing.T, r *gin.Engine, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
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
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// TestAPIKeyBearer Bearer API Key 通道：签发 / 作用域 / 轮换 / 吊销 / CSRF 例外。
func TestAPIKeyBearer(t *testing.T) {
	r := newTestServer(t)
	ck := adminSession(t, r)

	// 开启前台访问密码：此后读接口必须携带凭证，才能验证 Key 的读权限
	if w := doJSON(t, r, http.MethodPut, "/api/v1/auth/view",
		`{"enabled":true,"password":"viewpass"}`, ck); w.Code != http.StatusOK {
		t.Fatalf("开启访问密码 = %d %s", w.Code, w.Body.String())
	}

	// 未配置 Key 前，任意 Bearer 一律 401
	if w := doReq(t, r, http.MethodGet, "/api/v1/nodes", "",
		map[string]string{"Authorization": "Bearer cck_deadbeef"}); w.Code != http.StatusUnauthorized {
		t.Errorf("未配置时 Bearer = %d, want 401", w.Code)
	}
	// 签发需要管理员权限
	if w := doJSON(t, r, http.MethodPost, "/api/v1/auth/api-key/rotate", `{"scope":"admin"}`); w.Code != http.StatusUnauthorized {
		t.Errorf("匿名签发 = %d, want 401", w.Code)
	}
	// 初始状态：未设置
	if w := doJSON(t, r, http.MethodGet, "/api/v1/auth/api-key", "", ck); !strings.Contains(w.Body.String(), `"set":false`) {
		t.Errorf("初始状态 = %s", w.Body.String())
	}

	// 生成 admin Key
	w := doJSON(t, r, http.MethodPost, "/api/v1/auth/api-key/rotate", `{"scope":"admin"}`, ck)
	if w.Code != http.StatusOK {
		t.Fatalf("签发 = %d %s", w.Code, w.Body.String())
	}
	var issued struct {
		APIKey string `json:"api_key"`
		Scope  string `json:"scope"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &issued); err != nil || !strings.HasPrefix(issued.APIKey, "cck_") {
		t.Fatalf("签发结果异常: %s", w.Body.String())
	}
	if issued.Scope != "admin" {
		t.Errorf("scope = %s, want admin", issued.Scope)
	}
	bearer := map[string]string{"Authorization": "Bearer " + issued.APIKey}

	// 状态查询只回 set/scope，绝不回传明文
	w = doJSON(t, r, http.MethodGet, "/api/v1/auth/api-key", "", ck)
	if !strings.Contains(w.Body.String(), `"set":true`) || strings.Contains(w.Body.String(), issued.APIKey) {
		t.Errorf("状态查询异常: %s", w.Body.String())
	}

	// 读接口：无凭证 401，Bearer 放行
	if w := doJSON(t, r, http.MethodGet, "/api/v1/nodes", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("无凭证读 = %d, want 401", w.Code)
	}
	if w := doReq(t, r, http.MethodGet, "/api/v1/nodes", "", bearer); w.Code != http.StatusOK {
		t.Errorf("Bearer 读 = %d %s", w.Code, w.Body.String())
	}
	// whoami 回显凭证类型
	if w := doReq(t, r, http.MethodGet, "/api/v1/auth/whoami", "", bearer); !strings.Contains(w.Body.String(), `"via":"key"`) {
		t.Errorf("whoami = %s", w.Body.String())
	}
	// admin Key 可写
	if w := doReq(t, r, http.MethodPost, "/api/v1/nodes",
		`{"name":"via-key","os_type":"linux","access_mode":"agent"}`, bearer); w.Code != http.StatusOK {
		t.Errorf("Bearer 写 = %d %s", w.Code, w.Body.String())
	}
	// 带 Bearer 的跨站 Origin 不受 CSRF 拦截（凭证不由浏览器自动附带）
	if w := doReq(t, r, http.MethodPost, "/api/v1/nodes",
		`{"name":"via-key2","os_type":"linux","access_mode":"agent"}`,
		map[string]string{"Authorization": "Bearer " + issued.APIKey, "Origin": "https://evil.example"}); w.Code != http.StatusOK {
		t.Errorf("Bearer + 跨站 Origin = %d %s", w.Code, w.Body.String())
	}

	// 换成 view Key：读放行、写被拒
	w = doJSON(t, r, http.MethodPost, "/api/v1/auth/api-key/rotate", `{"scope":"view"}`, ck)
	var viewIssued struct {
		APIKey string `json:"api_key"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &viewIssued)
	viewBearer := map[string]string{"Authorization": "Bearer " + viewIssued.APIKey}
	if w := doReq(t, r, http.MethodGet, "/api/v1/nodes", "", viewBearer); w.Code != http.StatusOK {
		t.Errorf("view Key 读 = %d %s", w.Code, w.Body.String())
	}
	if w := doReq(t, r, http.MethodPost, "/api/v1/nodes",
		`{"name":"nope","os_type":"linux","access_mode":"agent"}`, viewBearer); w.Code != http.StatusUnauthorized {
		t.Errorf("view Key 写 = %d, want 401", w.Code)
	}
	// 非法 scope
	if w := doJSON(t, r, http.MethodPost, "/api/v1/auth/api-key/rotate", `{"scope":"root"}`, ck); w.Code != http.StatusBadRequest {
		t.Errorf("非法 scope = %d, want 400", w.Code)
	}

	// 轮换后旧 Key 立即失效
	if w := doReq(t, r, http.MethodGet, "/api/v1/nodes", "", bearer); w.Code != http.StatusUnauthorized {
		t.Errorf("轮换后旧 Key = %d, want 401", w.Code)
	}

	// WebSocket：query 传参兜底应通过鉴权层（httptest 无法真正升级，只断言不是 401）
	if w := doReq(t, r, http.MethodGet, "/api/v1/ws?access_token="+viewIssued.APIKey, "", nil); w.Code == http.StatusUnauthorized {
		t.Errorf("WS query token = %d, 不应 401", w.Code)
	}
	if w := doReq(t, r, http.MethodGet, "/api/v1/ws?access_token=cck_bogus", "", nil); w.Code != http.StatusUnauthorized {
		t.Errorf("WS 错误 token = %d, want 401", w.Code)
	}

	// 吊销后全部 Bearer 失效
	if w := doJSON(t, r, http.MethodDelete, "/api/v1/auth/api-key", "", ck); w.Code != http.StatusOK {
		t.Errorf("吊销 = %d %s", w.Code, w.Body.String())
	}
	if w := doReq(t, r, http.MethodGet, "/api/v1/nodes", "", viewBearer); w.Code != http.StatusUnauthorized {
		t.Errorf("吊销后 = %d, want 401", w.Code)
	}
}

// TestSessionStillWorks 确认改造后浏览器 Cookie 会话行为不变。
func TestSessionStillWorks(t *testing.T) {
	r := newTestServer(t)
	ck := adminSession(t, r)

	// 未开访问密码：读接口匿名放行
	if w := doJSON(t, r, http.MethodGet, "/api/v1/nodes", ""); w.Code != http.StatusOK {
		t.Errorf("匿名读（未开密码）= %d, want 200", w.Code)
	}
	// 会话 Cookie 仍具 admin 权限
	if w := doJSON(t, r, http.MethodGet, "/api/v1/auth/api-key", "", ck); w.Code != http.StatusOK {
		t.Errorf("Cookie 读 = %d", w.Code)
	}
	// whoami 仍是 cookie 身份
	if w := doJSON(t, r, http.MethodGet, "/api/v1/auth/whoami", "", ck); !strings.Contains(w.Body.String(), `"via":"cookie"`) {
		t.Errorf("whoami = %s", w.Body.String())
	}
	// 未开访问密码时，WS 匿名可连（鉴权层放行）
	if w := doJSON(t, r, http.MethodGet, "/api/v1/ws", ""); w.Code == http.StatusUnauthorized {
		t.Errorf("WS 匿名 = %d, 不应 401", w.Code)
	}
}

// TestPoolFilenameWithVersion 裸二进制文件名可带版本号（build.ps1 产物命名），
// 同时必须保持对旧版「无版本」命名的兼容（存量文件与手工上传）。
func TestPoolFilenameWithVersion(t *testing.T) {
	cases := []struct {
		name                       string
		component, goos, arch, ver string
		ok                         bool
	}{
		// 新命名（带版本）
		{"cc-agent-0.2.0-linux-amd64", "agent", "linux", "amd64", "0.2.0", true},
		{"cc-agent-0.2.0-windows-amd64.exe", "agent", "windows", "amd64", "0.2.0", true},
		{"cc-dashboard-0.2.0-linux-armv7", "server", "linux", "armv7", "0.2.0", true},
		{"cc-dashboard-0.2.0-windows-amd64.exe", "server", "windows", "amd64", "0.2.0", true},
		{"cc-agent-1.2.3-linux-arm64", "agent", "linux", "arm64", "1.2.3", true},
		// 旧命名（无版本）—— 必须继续识别
		{"cc-agent-linux-mipsle", "agent", "linux", "mipsle", "", true},
		{"cc-dashboard-linux-amd64", "server", "linux", "amd64", "", true},
		// 更早的旧名 cc-server（统一为 cc-dashboard 之前的产物）—— 仍需识别
		{"cc-server-0.2-linux-armv7", "server", "linux", "armv7", "0.2", true},
		{"cc-server-windows-amd64.exe", "server", "windows", "amd64", "", true},
		// 非法
		{"cc-agent-linux-riscv", "", "", "", "", false},
		{"random.bin", "", "", "", "", false},
	}
	for _, c := range cases {
		p, ok := parsePoolFilename(c.name)
		if ok != c.ok {
			t.Errorf("%s: ok=%v, want %v", c.name, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		if p.Component != c.component || p.GOOS != c.goos || p.Arch != c.arch || p.Version != c.ver {
			t.Errorf("%s → %+v, want {%s %s %s v=%s}", c.name, p, c.component, c.goos, c.arch, c.ver)
		}
	}
}

// TestMultiAPIKeys 多凭证：每台设备一条，各自吊销互不影响。
// 回归点：旧版只有一个 Key slot，任何一处 rotate 都会把其它设备踢下线。
func TestMultiAPIKeys(t *testing.T) {
	r := newTestServer(t)
	ck := adminSession(t, r)

	// 开访问密码：读接口必须带凭证，才能验证各 Key 的独立性
	if w := doJSON(t, r, http.MethodPut, "/api/v1/auth/view",
		`{"enabled":true,"password":"viewpass"}`, ck); w.Code != http.StatusOK {
		t.Fatalf("开启访问密码 = %d", w.Code)
	}

	type created struct {
		APIKey string `json:"api_key"`
		Key    struct {
			ID    int64  `json:"id"`
			Name  string `json:"name"`
			Scope string `json:"scope"`
		} `json:"key"`
	}
	var phone, tablet created
	w := doJSON(t, r, http.MethodPost, "/api/v1/auth/api-keys", `{"name":"phone","scope":"view"}`, ck)
	if w.Code != http.StatusOK {
		t.Fatalf("创建 phone = %d %s", w.Code, w.Body.String())
	}
	_ = json.Unmarshal(w.Body.Bytes(), &phone)
	w = doJSON(t, r, http.MethodPost, "/api/v1/auth/api-keys", `{"name":"tablet","scope":"view"}`, ck)
	_ = json.Unmarshal(w.Body.Bytes(), &tablet)
	if phone.APIKey == "" || tablet.APIKey == "" || phone.APIKey == tablet.APIKey {
		t.Fatalf("两个 Key 应各自独立: %+v %+v", phone, tablet)
	}

	// 两个 Key 同时可用
	for name, k := range map[string]string{"phone": phone.APIKey, "tablet": tablet.APIKey} {
		if w := doReq(t, r, http.MethodGet, "/api/v1/nodes", "",
			map[string]string{"Authorization": "Bearer " + k}); w.Code != http.StatusOK {
			t.Errorf("%s Key 读取 = %d, want 200", name, w.Code)
		}
	}

	// 吊销 phone，tablet 必须仍然可用（不再互踢）
	if w := doJSON(t, r, http.MethodDelete, "/api/v1/auth/api-keys/"+itoa(phone.Key.ID), "", ck); w.Code != http.StatusOK {
		t.Errorf("吊销 phone = %d %s", w.Code, w.Body.String())
	}
	if w := doReq(t, r, http.MethodGet, "/api/v1/nodes", "",
		map[string]string{"Authorization": "Bearer " + phone.APIKey}); w.Code != http.StatusUnauthorized {
		t.Errorf("已吊销 Key = %d, want 401", w.Code)
	}
	if w := doReq(t, r, http.MethodGet, "/api/v1/nodes", "",
		map[string]string{"Authorization": "Bearer " + tablet.APIKey}); w.Code != http.StatusOK {
		t.Errorf("另一台设备不应被踢下线 = %d, want 200", w.Code)
	}

	// 列表含剩余凭证，且不含明文
	w = doJSON(t, r, http.MethodGet, "/api/v1/auth/api-keys", "", ck)
	body := w.Body.String()
	if !strings.Contains(body, `"tablet"`) || strings.Contains(body, tablet.APIKey) {
		t.Errorf("列表内容异常: %s", body)
	}

	// 旧的单 Key 兼容接口仍可用，且与新表共存
	w = doJSON(t, r, http.MethodPost, "/api/v1/auth/api-key/rotate", `{"scope":"admin"}`, ck)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"api_key":"cck_`) {
		t.Errorf("兼容 rotate = %d %s", w.Code, w.Body.String())
	}
	var legacy struct {
		APIKey string `json:"api_key"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &legacy)
	if w := doReq(t, r, http.MethodGet, "/api/v1/nodes", "",
		map[string]string{"Authorization": "Bearer " + legacy.APIKey}); w.Code != http.StatusOK {
		t.Errorf("兼容 Key 读取 = %d", w.Code)
	}
	// rotate default 不影响 tablet
	if w := doReq(t, r, http.MethodGet, "/api/v1/nodes", "",
		map[string]string{"Authorization": "Bearer " + tablet.APIKey}); w.Code != http.StatusOK {
		t.Errorf("rotate default 后 tablet 应仍可用 = %d", w.Code)
	}
}

// TestLoginRateLimit 连续输错密码应触发 429，避免无限暴力破解。
func TestLoginRateLimit(t *testing.T) {
	r := newTestServer(t)
	adminSession(t, r) // 初始化管理员密码

	// 前 5 次失败：返回 401
	for i := 0; i < 5; i++ {
		if w := doJSON(t, r, http.MethodPost, "/api/v1/auth/login",
			`{"password":"wrong","scope":"admin"}`); w.Code != http.StatusUnauthorized {
			t.Fatalf("第 %d 次失败 = %d, want 401", i+1, w.Code)
		}
	}
	// 达到阈值后进入锁定 → 429
	w := doJSON(t, r, http.MethodPost, "/api/v1/auth/login", `{"password":"wrong","scope":"admin"}`)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("超过阈值 = %d, want 429（body=%s）", w.Code, w.Body.String())
	}
	if w.Header().Get("Retry-After") == "" {
		t.Error("429 应带 Retry-After 头")
	}
	// 锁定期间即使密码正确也被拒（防止锁定被绕过）
	w = doJSON(t, r, http.MethodPost, "/api/v1/auth/login", `{"password":"secret123","scope":"admin"}`)
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("锁定期间正确密码 = %d, want 429", w.Code)
	}
}
