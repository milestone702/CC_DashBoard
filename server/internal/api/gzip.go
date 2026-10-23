// 响应压缩：移动网络下显著减少传输量（JSON / HTML / JS / CSS 通常压到 1/4~1/6）。
// 跳过 WebSocket 升级、已压缩格式（图片/字体/压缩包）以及不支持 gzip 的客户端。
//
// 实现说明：gin 的 c.JSON 会先调用 WriteHeader 再设置 Content-Type，
// 因此在 WriteHeader 时无法判断类型。这里改为「进入 handler 前就声明压缩」，
// 通过 URL 后缀排除已压缩资源；无实体响应（204/304）再撤回声明。
package api

import (
	"compress/gzip"
	"net/http"
	"path/filepath"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
)

var gzipPool = sync.Pool{
	New: func() any { return gzip.NewWriter(nil) },
}

// alreadyCompressed 本身已压缩的扩展名，再压只会浪费 CPU。
var alreadyCompressed = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".ico": true,
	".woff": true, ".woff2": true, ".ttf": true, ".eot": true,
	".gz": true, ".zip": true, ".br": true, ".7z": true, ".mp4": true,
}

func skipCompress(path string) bool {
	return alreadyCompressed[strings.ToLower(filepath.Ext(path))]
}

type gzipWriter struct {
	gin.ResponseWriter
	gz *gzip.Writer
}

// prepare 清掉与压缩流冲突的响应头。
//
// gin 的 render.Data（c.Data，静态资源走的就是它）和 http.ServeContent（c.File）
// 都会在调用 Write 之前设置「未压缩」的 Content-Length，而 render.Data 不经过
// WriteHeader——只在此时删除才有效。否则客户端按原始长度读取压缩流，判定响应
// 被截断（浏览器里就是脚本加载失败、整页空白）。
// Accept-Ranges 一并对齐：压缩后的字节区间与原文件不再对应。
func (w *gzipWriter) prepare() {
	h := w.Header()
	h.Del("Content-Length")
	h.Del("Accept-Ranges")
}

func (w *gzipWriter) WriteHeader(code int) {
	// 无实体响应不能带 Content-Encoding；有实体时压缩后长度未知，去掉原 Content-Length
	if code == http.StatusNoContent || code == http.StatusNotModified {
		w.Header().Del("Content-Encoding")
		w.Header().Del("Vary")
	} else {
		w.prepare()
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *gzipWriter) WriteHeaderNow() {
	w.prepare()
	w.ResponseWriter.WriteHeaderNow()
}

func (w *gzipWriter) Write(b []byte) (int, error) {
	w.prepare()
	return w.gz.Write(b)
}

func (w *gzipWriter) WriteString(s string) (int, error) {
	w.prepare()
	return w.gz.Write([]byte(s))
}

// gzipMiddleware 按需压缩响应体。
func gzipMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetHeader("Upgrade") != "" ||
			// Range 请求（断点续传 / 视频拖动）压缩后区间语义会错，直接放行
			c.GetHeader("Range") != "" ||
			!strings.Contains(c.GetHeader("Accept-Encoding"), "gzip") ||
			skipCompress(c.Request.URL.Path) {
			c.Next()
			return
		}
		gz := gzipPool.Get().(*gzip.Writer)
		gz.Reset(c.Writer)
		defer func() {
			_ = gz.Close() // flush 压缩尾部
			gzipPool.Put(gz)
		}()
		c.Header("Content-Encoding", "gzip")
		c.Header("Vary", "Accept-Encoding")
		c.Writer = &gzipWriter{ResponseWriter: c.Writer, gz: gz}
		c.Next()
	}
}
