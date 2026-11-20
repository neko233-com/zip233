// Package server 提供本地 HTTP 服务与单一 JSON-RPC 风格的 /api/invoke 接口。
// 它只依赖 contract.Service，不直接引用 archive/update/shell 等实现包。
package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/neko233-com/zip233/internal/contract"
	"github.com/neko233-com/zip233/internal/version"
	"github.com/neko233-com/zip233/web"
)

// Server 包装一个 contract.Service 并对外提供 HTTP handler。
type Server struct {
	svc contract.Service
}

// New 创建一个 Server。
func New(svc contract.Service) *Server {
	return &Server{svc: svc}
}

// Handler 返回路由：
//
//	GET  /api/health  -> {"ok":true}
//	POST /api/invoke  -> {"cmd":"...","payload":{...}}
//	GET  /            -> 嵌入的 index.html
//	GET  /<resource>  -> 嵌入的静态资源
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", s.health)
	mux.HandleFunc("/api/invoke", s.invoke)
	mux.HandleFunc("/", s.static)
	return mux
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type invokeRequest struct {
	Cmd     string                     `json:"cmd"`
	Payload map[string]json.RawMessage `json:"payload"`
}

func (s *Server) invoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		writeFail(w, "读取请求失败: "+err.Error())
		return
	}
	var req invokeRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeFail(w, "请求格式错误: "+err.Error())
		return
	}
	data, err := s.dispatch(r, req.Cmd, req.Payload)
	if err != nil {
		writeFail(w, err.Error())
		return
	}
	writeOK(w, data)
}

func (s *Server) dispatch(r *http.Request, cmd string, p map[string]json.RawMessage) (any, error) {
	ctx := r.Context()
	switch cmd {
	case "pick_archive":
		v, err := s.svc.PickArchive()
		return nullableString(v), err
	case "pick_sources":
		v, err := s.svc.PickSources()
		if err != nil {
			return nil, err
		}
		if v == nil {
			v = []string{}
		}
		return v, nil
	case "pick_source_folder":
		v, err := s.svc.PickSourceFolder()
		return nullableString(v), err
	case "pick_output_folder":
		v, err := s.svc.PickOutputFolder()
		return nullableString(v), err
	case "pick_update_package":
		v, err := s.svc.PickUpdatePackage()
		return nullableString(v), err
	case "save_archive":
		v, err := s.svc.SaveArchive(getString(p, "defaultName"), getString(p, "extension"))
		return nullableString(v), err
	case "list_archive":
		return s.svc.ListArchive(getString(p, "path"), getString(p, "password"))
	case "test_archive":
		return s.svc.TestArchive(getString(p, "path"), getString(p, "password"))
	case "extract_archive":
		return s.svc.ExtractArchive(getString(p, "path"), getString(p, "outputDir"), getString(p, "password"))
	case "create_archive":
		return s.svc.CreateArchive(getStrings(p, "paths"), getString(p, "target"), getString(p, "format"),
			getString(p, "level"), getString(p, "password"))
	case "shell_integration_status":
		return s.svc.ShellStatus(), nil
	case "register_shell_integration", "repair_shell_integration":
		return s.svc.RegisterShell()
	case "unregister_shell_integration":
		return s.svc.UnregisterShell()
	case "get_launch_request":
		return s.svc.LaunchRequest(), nil
	case "check_updates":
		return s.svc.CheckUpdates(ctx)
	case "download_update":
		return s.svc.StartDownload(ctx)
	case "update_download_status":
		return s.svc.DownloadStatus(), nil
	case "cancel_update_download":
		if err := s.svc.CancelDownload(); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true}, nil
	case "install_update":
		if err := s.svc.InstallUpdate(ctx); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true}, nil
	case "install_local_update":
		if err := s.svc.InstallLocalUpdate(ctx, getString(p, "path")); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true}, nil
	case "set_auto_check":
		s.svc.SetAutoCheck(getBool(p, "enabled"))
		return map[string]any{"ok": true}, nil
	case "auto_check_enabled":
		return map[string]any{"enabled": s.svc.AutoCheckEnabled()}, nil
	case "version":
		return map[string]any{"version": version.Version}, nil
	default:
		return nil, fmt.Errorf("未知命令: %s", cmd)
	}
}

func (s *Server) static(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}
	// 防止路径穿越：只允许嵌入资源内的普通文件名。
	if path != "index.html" && path != "styles.css" && path != "app.js" {
		http.NotFound(w, r)
		return
	}
	data, err := web.FS.ReadFile(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	switch path {
	case "index.html":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	case "styles.css":
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	case "app.js":
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	}
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(data)
}

// ---------- payload 读取 ----------

func getString(p map[string]json.RawMessage, key string) string {
	if raw, ok := p[key]; ok {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
	}
	return ""
}

func getBool(p map[string]json.RawMessage, key string) bool {
	if raw, ok := p[key]; ok {
		var b bool
		if json.Unmarshal(raw, &b) == nil {
			return b
		}
	}
	return false
}

func getStrings(p map[string]json.RawMessage, key string) []string {
	if raw, ok := p[key]; ok {
		var s []string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
	}
	return []string{}
}

func nullableString(v *string) any {
	if v == nil {
		return nil
	}
	return *v
}

// ---------- 响应 ----------

type invokeResponse struct {
	Success bool `json:"success"`
	Data    any  `json:"data"`
}

func writeOK(w http.ResponseWriter, data any) {
	writeJSON(w, http.StatusOK, invokeResponse{Success: true, Data: data})
}

func writeFail(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusOK, invokeResponse{
		Success: false,
		Data:    map[string]any{"error": msg},
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func methodNotAllowed(w http.ResponseWriter) {
	writeJSON(w, http.StatusMethodNotAllowed, invokeResponse{
		Success: false,
		Data:    map[string]any{"error": "请求方法不被允许"},
	})
}
