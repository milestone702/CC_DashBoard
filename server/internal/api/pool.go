// 程序池：上传 / 列表 / 删除 / 下载与 OTA 总开关（契约见 docs/API.md）。
package api

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"cc_dashboard/server/internal/model"
)

// Version 服务端版本，cmd/master 启动时注入（管理页与池中 server 版本对比提示）。
var Version = "dev"

const (
	maxPoolUpload = 256 << 20 // 单文件上限 256MB
	otaSettingKey = "ota_enabled"
)

var poolNameSanitize = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func (s *Server) poolDir() string {
	dir := filepath.Join(s.db.DataDir(), "pool")
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

func (s *Server) otaEnabled() bool {
	v, _, _ := s.db.GetSetting(otaSettingKey)
	return v == "1"
}

// handlePoolList 管理员：池清单 + OTA 开关 + 当前服务端版本。
func (s *Server) handlePoolList(c *gin.Context) {
	items, err := s.db.ListPoolItems("")
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	if items == nil {
		items = []*model.PoolItem{}
	}
	c.JSON(http.StatusOK, gin.H{
		"items":         items,
		"ota_enabled":   s.otaEnabled(),
		"server_version": Version,
	})
}

// handlePoolOTASet 管理员：OTA 总开关（默认关，打开后才下发升级指令）。
func (s *Server) handlePoolOTASet(c *gin.Context) {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		fail(c, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	val := "0"
	if body.Enabled {
		val = "1"
	}
	if err := s.db.SetSetting(otaSettingKey, val); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "ota_enabled": body.Enabled})
}

// handlePoolUpload 管理员：multipart 上传程序文件（表单字段 component/version/goos/arch/format/note）。
func (s *Server) handlePoolUpload(c *gin.Context) {
	if err := c.Request.ParseMultipartForm(maxPoolUpload); err != nil {
		fail(c, http.StatusBadRequest, "multipart 解析失败: "+err.Error())
		return
	}
	component := strings.TrimSpace(c.PostForm("component"))
	version := strings.TrimSpace(c.PostForm("version"))
	goos := strings.ToLower(strings.TrimSpace(c.PostForm("goos")))
	arch := strings.ToLower(strings.TrimSpace(c.PostForm("arch")))
	format := strings.ToLower(strings.TrimSpace(c.PostForm("format")))
	note := strings.TrimSpace(c.PostForm("note"))
	if component != "agent" && component != "server" {
		fail(c, http.StatusBadRequest, "component 必须为 agent 或 server")
		return
	}
	if version == "" {
		fail(c, http.StatusBadRequest, "version 必填")
		return
	}
	if format == "" {
		format = "bin"
	}
	switch format {
	case "bin", "deb", "rpm", "ipk":
	default:
		fail(c, http.StatusBadRequest, "format 必须为 bin/deb/rpm/ipk")
		return
	}
	fh, err := c.FormFile("file")
	if err != nil {
		fail(c, http.StatusBadRequest, "缺少文件字段 file: "+err.Error())
		return
	}
	item, err := s.savePoolFile(fh, component, ensureVersionV(version), goos, arch, format, note)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"item": item})
}

// savePoolFile 流式落盘 + sha256 内容寻址命名 + 元数据入库（单文件上传与批量上传共用）。
func (s *Server) savePoolFile(fh *multipart.FileHeader, component, version, goos, arch, format, note string) (*model.PoolItem, error) {
	src, err := fh.Open()
	if err != nil {
		return nil, err
	}
	defer src.Close()

	safeBase := poolNameSanitize.ReplaceAllString(filepath.Base(fh.Filename), "_")
	if safeBase == "" || safeBase == "_" {
		return nil, fmt.Errorf("文件名无效: %s", fh.Filename)
	}
	tmp, err := os.CreateTemp(s.poolDir(), ".upload-*")
	if err != nil {
		return nil, fmt.Errorf("创建临时文件失败: %w", err)
	}
	tmpPath := tmp.Name()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(src, maxPoolUpload+1))
	closeErr := tmp.Close()
	if err != nil || closeErr != nil {
		_ = os.Remove(tmpPath)
		return nil, fmt.Errorf("写入失败: %v", errString(err, closeErr))
	}
	if n > maxPoolUpload {
		_ = os.Remove(tmpPath)
		return nil, fmt.Errorf("文件超过 256MB 上限")
	}
	sum := h.Sum(nil)
	sha := hex.EncodeToString(sum)
	filename := sha[:16] + "_" + safeBase
	finalPath := filepath.Join(s.poolDir(), filename)
	if err := os.Rename(tmpPath, finalPath); err != nil {
		_ = os.Remove(tmpPath)
		return nil, fmt.Errorf("落盘失败: %w", err)
	}
	it := &model.PoolItem{
		Component: component, Version: version, GOOS: goos, Arch: arch,
		Format: format, Filename: filename, SHA256: sha, Size: n, Note: note,
	}
	id, err := s.db.InsertPoolItem(it)
	if err != nil {
		_ = os.Remove(finalPath)
		return nil, err
	}
	it.ID = id
	return it, nil
}

// handlePoolDelete 管理员：删除条目及其文件。
func (s *Server) handlePoolDelete(c *gin.Context) {
	id, err := parseID(c)
	if err != nil {
		return
	}
	it, err := s.db.GetPoolItem(id)
	if err != nil {
		fail(c, http.StatusNotFound, "not found")
		return
	}
	if err := s.db.DeletePoolItem(id); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	// 只取文件名基名，防路径穿越
	_ = os.Remove(filepath.Join(s.poolDir(), filepath.Base(it.Filename)))
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// handlePoolDownload 节点 Token 鉴权：Agent OTA 拉取安装包。
func (s *Server) handlePoolDownload(c *gin.Context) {
	node, err := s.db.FindNodeByToken(c.GetHeader("X-CC-Node-Token"))
	if err != nil || !node.Enabled || !node.Approved {
		fail(c, http.StatusUnauthorized, "invalid token")
		return
	}
	id, err := parseID(c)
	if err != nil {
		return
	}
	it, err := s.db.GetPoolItem(id)
	if err != nil {
		fail(c, http.StatusNotFound, "not found")
		return
	}
	path := filepath.Join(s.poolDir(), filepath.Base(it.Filename))
	if _, err := os.Stat(path); err != nil {
		fail(c, http.StatusNotFound, "file missing")
		return
	}
	c.File(path)
}

// agentOTAUpgrade 计算 OTA 升级指令目标（nil = 不升级）：
// force（手动触发）时忽略全局开关，其余条件不变——平台匹配 + 版本不同 + bin 格式。
func (s *Server) agentOTAUpgrade(goos, arch, currentVersion string, force bool) *model.PoolItem {
	if goos == "" || arch == "" {
		return nil
	}
	if !force && !s.otaEnabled() {
		return nil
	}
	latest, err := s.db.PoolLatest("agent", goos, arch, "bin")
	if err != nil || latest == nil || sameVersion(latest.Version, currentVersion) {
		return nil
	}
	return latest
}

// normVersion 版本比较归一化：去空白与 v/V 前缀（"V0.3" 与 "0.3" 视为相同）。
func normVersion(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 1 && (s[0] == 'v' || s[0] == 'V') {
		return s[1:]
	}
	return s
}

// sameVersion 归一化后比较两个版本号是否相同（防止 V 前缀差异导致“永远可升级”循环）。
func sameVersion(a, b string) bool {
	return strings.EqualFold(normVersion(a), normVersion(b))
}

// ensureVersionV 展示统一：数字开头的版本补 V 前缀（"0.3" → "V0.3"；"dev" 等保持原样）。
func ensureVersionV(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	if s[0] == 'v' || s[0] == 'V' {
		return "V" + s[1:]
	}
	if s[0] >= '0' && s[0] <= '9' {
		return "V" + s
	}
	return s
}

// handleForceUpgrade 管理员：手动触发单节点 OTA（不受全局开关限制，节点下次上报时生效）。
func (s *Server) handleForceUpgrade(c *gin.Context) {
	n, ok := s.nodeByID(c)
	if !ok {
		return
	}
	if n.AccessMode != model.ModeAgent {
		fail(c, http.StatusBadRequest, "仅 Agent 接入的节点支持 OTA 升级")
		return
	}
	if n.OSType == model.OSTypeOpenWrt {
		fail(c, http.StatusBadRequest, "OpenWrt 节点仅提示、不执行自动升级")
		return
	}
	if n.AgentVersion == "" || n.AgentGoos == "" || n.AgentArch == "" {
		fail(c, http.StatusBadRequest, "节点尚未上报版本/平台信息（等待 Agent 首次上报后再试）")
		return
	}
	up, err := s.db.PoolLatest("agent", n.AgentGoos, n.AgentArch, "bin")
	if err != nil || up == nil {
		fail(c, http.StatusBadRequest, "程序池中没有该平台的 bin 版本，请先批量上传对应产物")
		return
	}
	if sameVersion(up.Version, n.AgentVersion) {
		fail(c, http.StatusBadRequest, "已是最新版本 "+up.Version)
		return
	}
	s.upMu.Lock()
	s.upgradeForce[n.ID] = true
	s.upMu.Unlock()
	c.JSON(http.StatusOK, gin.H{"ok": true, "version": up.Version, "sha256": up.SHA256})
}

// consumeUpgradeForce 取出并清除手动升级标记（下次 report 消费一次）。
func (s *Server) consumeUpgradeForce(id int64) bool {
	s.upMu.Lock()
	defer s.upMu.Unlock()
	if s.upgradeForce[id] {
		delete(s.upgradeForce, id)
		return true
	}
	return false
}

// ---------- 批量上传：从文件名自动解析 元数据 ----------

// build.ps1 产物命名约定：
//
//	cc-agent-linux-amd64 / cc-server-windows-amd64.exe        → 裸二进制（bin，无版本号）
//	cc-agent_<ver>_<arch>.deb                                  → deb
//	cc-agent-<ver>-1.<arch>.rpm                                → rpm
//	cc-agent-<ver>-<opkg_arch>.ipk                             → ipk
var (
	rePoolDeb = regexp.MustCompile(`^cc-(agent|dashboard)_([^_]+)_(amd64|arm64)\.deb$`)
	rePoolRpm = regexp.MustCompile(`^cc-(agent|dashboard)-(.+?)-1\.(x86_64|aarch64)\.rpm$`)
	rePoolIpk = regexp.MustCompile(`^cc-agent-(.+?)-(mipsel_24kc|arm_cortex-a7|aarch64_cortex-a53|x86_64)\.ipk$`)
	rePoolRaw = regexp.MustCompile(`^cc-(agent|server|dashboard)-(linux|windows)-(amd64|arm64|armv7|mipsle)(\.exe)?$`)
)

type poolParsed struct {
	Component string
	GOOS      string
	Arch      string
	Format    string
	Version   string // 文件名中解析出的版本；裸二进制为空 → 用兜底版本
}

func poolComponentOf(pkg string) string {
	if pkg == "agent" {
		return "agent"
	}
	return "server" // server / dashboard 同义
}

// parsePoolFilename 按构建产物命名约定解析元数据；无法识别返回 false。
func parsePoolFilename(name string) (poolParsed, bool) {
	base := filepath.Base(name)
	if m := rePoolDeb.FindStringSubmatch(base); m != nil {
		return poolParsed{Component: poolComponentOf(m[1]), GOOS: "linux", Arch: m[3], Format: "deb", Version: m[2]}, true
	}
	if m := rePoolRpm.FindStringSubmatch(base); m != nil {
		arch := m[3]
		if arch == "x86_64" {
			arch = "amd64"
		} else {
			arch = "arm64" // aarch64
		}
		return poolParsed{Component: poolComponentOf(m[1]), GOOS: "linux", Arch: arch, Format: "rpm", Version: m[2]}, true
	}
	if m := rePoolIpk.FindStringSubmatch(base); m != nil {
		arch := map[string]string{
			"mipsel_24kc": "mipsle", "arm_cortex-a7": "armv7",
			"aarch64_cortex-a53": "arm64", "x86_64": "amd64",
		}[m[2]]
		return poolParsed{Component: "agent", GOOS: "linux", Arch: arch, Format: "ipk", Version: m[1]}, true
	}
	if m := rePoolRaw.FindStringSubmatch(base); m != nil {
		return poolParsed{Component: poolComponentOf(m[1]), GOOS: m[2], Arch: m[3], Format: "bin"}, true
	}
	return poolParsed{}, false
}

// handlePoolBatch 管理员：一次性上传整批构建产物，按文件名自动解析元数据。
// 表单：files[]（多文件）+ version（兜底版本，用于文件名不带版本的裸二进制）。
func (s *Server) handlePoolBatch(c *gin.Context) {
	if err := c.Request.ParseMultipartForm(maxPoolUpload); err != nil {
		fail(c, http.StatusBadRequest, "multipart 解析失败: "+err.Error())
		return
	}
	fallback := strings.TrimSpace(c.PostForm("version"))
	form, err := c.MultipartForm()
	var files []*multipart.FileHeader
	if err == nil && form != nil {
		files = form.File["files"]
		if len(files) == 0 {
			// 兼容单字段名 file
			files = form.File["file"]
		}
	}
	if len(files) == 0 {
		fail(c, http.StatusBadRequest, "缺少文件字段 files")
		return
	}
	if len(files) > 64 {
		fail(c, http.StatusBadRequest, "单次最多 64 个文件")
		return
	}
	uploaded := make([]gin.H, 0, len(files))
	skipped := make([]gin.H, 0)
	for _, fh := range files {
		p, ok := parsePoolFilename(fh.Filename)
		if !ok {
			skipped = append(skipped, gin.H{"name": fh.Filename,
				"reason": "文件名无法识别（需 cc-agent / cc-server / cc-dashboard 前缀）"})
			continue
		}
		ver := p.Version
		if ver == "" {
			ver = fallback
		}
		if ver == "" {
			skipped = append(skipped, gin.H{"name": fh.Filename,
				"reason": "文件名不含版本号（裸二进制），请填写兜底版本号"})
			continue
		}
		item, err := s.savePoolFile(fh, p.Component, ensureVersionV(ver), p.GOOS, p.Arch, p.Format, "")
		if err != nil {
			skipped = append(skipped, gin.H{"name": fh.Filename, "reason": err.Error()})
			continue
		}
		uploaded = append(uploaded, gin.H{"name": fh.Filename, "item": item})
	}
	c.JSON(http.StatusOK, gin.H{"uploaded": uploaded, "skipped": skipped})
}

// attachUpgrade 为节点附加“可升级”提示（任意格式的池条目即可提示，不只限 bin）。
func (s *Server) attachUpgrade(n *model.Node) {
	if n == nil || n.AccessMode != model.ModeAgent || n.AgentVersion == "" ||
		n.AgentGoos == "" || n.AgentArch == "" {
		return
	}
	latest, err := s.db.PoolLatest("agent", n.AgentGoos, n.AgentArch, "")
	if err != nil || latest == nil || sameVersion(latest.Version, n.AgentVersion) {
		return
	}
	n.Upgrade = &model.UpgradeHint{Version: latest.Version, PoolID: latest.ID}
}

func errString(errs ...error) string {
	parts := make([]string, 0, len(errs))
	for _, e := range errs {
		if e != nil {
			parts = append(parts, e.Error())
		}
	}
	return strings.Join(parts, "; ")
}

// poolDownloadURL 生成 Agent 下载相对路径。
func poolDownloadURL(id int64) string {
	return "/api/v1/pool/" + strconv.FormatInt(id, 10) + "/download"
}
