// Package web 承载桌面 UI 的静态资源，编译期嵌入二进制。
package web

import "embed"

//go:embed index.html styles.css app.js
var FS embed.FS
