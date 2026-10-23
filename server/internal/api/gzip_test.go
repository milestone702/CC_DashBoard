package api

import (
	"bytes"
	"compress/gzip"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"cc_dashboard/server/internal/webui"
)

// embeddedAsset 取一个真实存在的嵌入静态资源（名带内容 hash，故运行时枚举）。
func embeddedAsset(t *testing.T) (string, []byte) {
	t.Helper()
	fsys, err := fs.Sub(webui.Dist, "dist")
	if err != nil {
		t.Fatalf("sub dist: %v", err)
	}
	entries, err := fs.ReadDir(fsys, "assets")
	if err != nil || len(entries) == 0 {
		t.Skipf("无嵌入前端产物（先构建 web/dist）: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := fs.ReadFile(fsys, "assets/"+e.Name())
		if err != nil {
			t.Fatalf("read asset %s: %v", e.Name(), err)
		}
		return e.Name(), data
	}
	t.Skip("嵌入产物中没有 assets 文件")
	return "", nil
}

// TestGzipStaticResourceNotTruncated 回归：静态资源经 gzip 后不能还留着「未压缩长度」的
// Content-Length。gin 的 c.Data 会在 Write 前写 Content-Length 且不经过 WriteHeader，
// 旧实现在 WriteHeader 里删长度根本不会触发，客户端按原始长度读压缩流即判定截断
// （浏览器表现为脚本加载失败、整页空白）。
func TestGzipStaticResourceNotTruncated(t *testing.T) {
	r := newTestServer(t)
	name, want := embeddedAsset(t)

	req := httptest.NewRequest(http.MethodGet, "/assets/"+name, nil)
	req.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := w.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	body := w.Body.Bytes()
	if cl := w.Header().Get("Content-Length"); cl != "" {
		t.Fatalf("gzip 响应仍带 Content-Length=%s（原始 %d 字节，压缩后 %d 字节），客户端会判定截断",
			cl, len(want), len(body))
	}

	zr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	defer zr.Close()
	got, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("gunzip: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("解压内容与源文件不一致：%d vs %d 字节", len(got), len(want))
	}
}

// TestGzipAbsentWithoutNegotiation 未协商 gzip 时保持原样（长度与内容都不变）。
func TestGzipAbsentWithoutNegotiation(t *testing.T) {
	r := newTestServer(t)
	name, want := embeddedAsset(t)

	req := httptest.NewRequest(http.MethodGet, "/assets/"+name, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if got := w.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("未协商时不应压缩，Content-Encoding = %q", got)
	}
	if got := w.Body.Len(); got != len(want) {
		t.Fatalf("body = %d 字节, want %d", got, len(want))
	}
	if cl := w.Header().Get("Content-Length"); cl != "" && cl != strconv.Itoa(len(want)) {
		t.Fatalf("Content-Length = %s, want %d 或空", cl, len(want))
	}
}

// TestGzipSkipsRangeRequest Range 请求（断点续传）不能被压缩，否则区间语义错乱。
func TestGzipSkipsRangeRequest(t *testing.T) {
	r := newTestServer(t)
	name, _ := embeddedAsset(t)

	req := httptest.NewRequest(http.MethodGet, "/assets/"+name, nil)
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("Range", "bytes=0-9")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if got := w.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("Range 请求不应压缩，Content-Encoding = %q", got)
	}
}
