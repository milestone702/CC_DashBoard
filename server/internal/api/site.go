// 站点标题（页面左上角品牌名）：公共可读（登录页也要显示）、管理员可改。
package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

const (
	keySiteTitle     = "site_title"
	keyOverviewCols  = "overview_cols"
	defaultSiteTitle = "CC_DashBoard"
)

// handleGetSite GET /api/v1/site
func (s *Server) handleGetSite(c *gin.Context) {
	v, ok, _ := s.db.GetSetting(keySiteTitle)
	if !ok || strings.TrimSpace(v) == "" {
		v = defaultSiteTitle
	}
	c.JSON(http.StatusOK, gin.H{"title": v})
}

// handleSetSite PUT /api/v1/site {title} — 空标题恢复默认。
func (s *Server) handleSetSite(c *gin.Context) {
	var body struct {
		Title string `json:"title"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		fail(c, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	title := strings.TrimSpace(body.Title)
	if r := []rune(title); len(r) > 32 {
		title = string(r[:32]) // 按字符截断，避免多字节被劈开
	}
	if err := s.db.SetSetting(keySiteTitle, title); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	if title == "" {
		title = defaultSiteTitle
	}
	c.JSON(http.StatusOK, gin.H{"title": title})
}

// validOverviewCols 概览每行卡片数：0=响应式默认，其余为固定列数。
func validOverviewCols(n int) bool {
	return n == 0 || n == 2 || n == 3 || n == 4 || n == 6
}

// handleGetOverviewCols GET /api/v1/overview/cols — 存数据库，换设备/清缓存不丢。
func (s *Server) handleGetOverviewCols(c *gin.Context) {
	v, _, _ := s.db.GetSetting(keyOverviewCols)
	cols := 0
	if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && validOverviewCols(n) {
		cols = n
	}
	c.JSON(http.StatusOK, gin.H{"cols": cols})
}

// handleSetOverviewCols PUT /api/v1/overview/cols {cols: 0|2|3|4|6}
func (s *Server) handleSetOverviewCols(c *gin.Context) {
	var body struct {
		Cols int `json:"cols"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		fail(c, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if !validOverviewCols(body.Cols) {
		fail(c, http.StatusBadRequest, "cols 仅支持 0/2/3/4/6")
		return
	}
	if err := s.db.SetSetting(keyOverviewCols, strconv.Itoa(body.Cols)); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"cols": body.Cols})
}
