// Package webbox 表格工具箱的前端静态资源，由 cmd/toolbox 内嵌进 exe。
package webbox

import "embed"

//go:embed index.html app.js style.css
var FS embed.FS
