// 登录限流：阻挡在线暴力破解密码。
// bcrypt 只降低单次尝试成本，无法阻止无限重试；这里按客户端 IP 记录连续失败，
// 失败越多锁定越久（递增退避），成功登录后清零。
package api

import (
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// 失败次数 → 锁定时长（递增退避）。达到阈值后按最后一档持续锁定。
var loginLockSteps = []struct {
	fails int
	lock  time.Duration
}{
	{5, 1 * time.Minute},
	{10, 5 * time.Minute},
	{15, 30 * time.Minute},
}

// loginEntry 单个客户端的失败状态。
type loginEntry struct {
	fails int
	last  time.Time // 最后一次失败时间（用于清理长期不活跃条目）
	until time.Time // 锁定截止时刻
}

// loginLimiter 内存态限流器（进程内，重启即清零）。
type loginLimiter struct {
	mu      sync.Mutex
	entries map[string]*loginEntry
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{entries: map[string]*loginEntry{}}
}

// allowed 判断当前是否允许尝试；不允许时返回需等待的时长。
func (l *loginLimiter) allowed(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.entries[key]
	if !ok {
		return true, 0
	}
	if wait := time.Until(e.until); wait > 0 {
		return false, wait
	}
	return true, 0
}

// fail 记录一次失败，达到阈值则进入锁定。
func (l *loginLimiter) fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	e, ok := l.entries[key]
	if !ok {
		e = &loginEntry{}
		l.entries[key] = e
	}
	e.fails++
	e.last = now
	for _, s := range loginLockSteps {
		if e.fails >= s.fails {
			e.until = now.Add(s.lock)
		}
	}
	l.sweepLocked(now)
}

// success 登录成功后清除失败计数。
func (l *loginLimiter) success(key string) {
	l.mu.Lock()
	delete(l.entries, key)
	l.mu.Unlock()
}

// sweepLocked 清理长期不活跃的条目，避免被大量伪造源 IP 撑爆内存（调用方已持锁）。
func (l *loginLimiter) sweepLocked(now time.Time) {
	if len(l.entries) < 512 {
		return
	}
	for k, e := range l.entries {
		if now.Sub(e.last) > time.Hour && now.After(e.until) {
			delete(l.entries, k)
		}
	}
}

// clientKey 取限流维度：直连 IP；仅当直连方是回环/内网（反代）时才采信
// X-Forwarded-For / X-Real-IP，防止外网客户端伪造头绕过限流。
func clientKey(c *gin.Context) string {
	host, _, err := net.SplitHostPort(c.Request.RemoteAddr)
	if err != nil {
		host = c.Request.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil || !(ip.IsLoopback() || ip.IsPrivate()) {
		return host
	}
	if xff := c.GetHeader("X-Forwarded-For"); xff != "" {
		if first := strings.TrimSpace(strings.Split(xff, ",")[0]); first != "" {
			return first
		}
	}
	if xr := c.GetHeader("X-Real-IP"); xr != "" {
		return strings.TrimSpace(xr)
	}
	return host
}

// loginGuard 登录/初始化接口的前置限流中间件。
func (s *Server) loginGuard() gin.HandlerFunc {
	return func(c *gin.Context) {
		key := clientKey(c)
		if ok, wait := s.limiter.allowed(key); !ok {
			secs := int(wait.Seconds()) + 1
			c.Header("Retry-After", strconv.Itoa(secs))
			fail(c, http.StatusTooManyRequests,
				fmt.Sprintf("尝试过于频繁，请 %d 秒后重试", secs))
			c.Abort()
			return
		}
		c.Next()
	}
}

// loginFailed 供 handler 在密码校验失败时调用。
func (s *Server) loginFailed(c *gin.Context) { s.limiter.fail(clientKey(c)) }

// loginSucceeded 供 handler 在登录成功时调用。
func (s *Server) loginSucceeded(c *gin.Context) { s.limiter.success(clientKey(c)) }
