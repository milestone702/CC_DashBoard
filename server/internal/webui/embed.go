// 编译嵌入的前端产物。
// 由 build.ps1 在构建前将 web/dist 复制到 ./dist；dist/.gitkeep 保证空目录可编译。
package webui

import "embed"

// Dist 内含前端构建产物（index.html、assets/…）。
//
//go:embed all:dist
var Dist embed.FS
