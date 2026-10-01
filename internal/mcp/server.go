package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mcpdev/internal/auth"
	"mcpdev/internal/command"
	"mcpdev/internal/config"
	"mcpdev/internal/files"
	gitservice "mcpdev/internal/git"
	openaiservice "mcpdev/internal/openai"
	"mcpdev/internal/search"
	"mcpdev/internal/workspace"
)

const Version = "1.1.1"
const LatestProtocol = "2026-07-28"

var downloadableFiles = map[string]struct{}{
	"mcp-server-windows-amd64.exe": {},
	"mcp-server-windows-arm64.exe": {},
	"mcp-server-linux-amd64":       {},
	"mcp-server-linux-arm64":       {},
	"mcp-server-macos-amd64":       {},
	"mcp-server-macos-arm64":       {},
	"SHA256SUMS":                   {},
}

type Server struct {
	cfg           *config.Config
	auth          *auth.Middleware
	ws            *workspace.Manager
	files         *files.Service
	search        *search.Service
	runner        *command.Runner
	git           *gitservice.Service
	openAI        *openaiservice.Client
	openAIAuth    *auth.HeaderPair
	openAILimiter *requestLimiter
	started       time.Time
	logger        *slog.Logger
}
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}
type Tool struct {
	Name        string         `json:"name"`
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations map[string]any `json:"annotations,omitempty"`
}

func New(c *config.Config, logger *slog.Logger) *Server {
	ws := workspace.New(c.Workspaces)
	runner := &command.Runner{Workspaces: ws, MaxOutput: c.Limits.MaxOutputBytes, DefaultTimeout: time.Duration(c.Limits.CommandTimeout) * time.Second}
	s := &Server{cfg: c, auth: auth.New(c.Security.Token), ws: ws, files: &files.Service{Workspaces: ws, MaxRead: c.Limits.MaxReadBytes, MaxWrite: c.Limits.MaxWriteBytes}, search: &search.Service{Workspaces: ws, MaxResults: c.Limits.MaxSearchResult}, runner: runner, git: &gitservice.Service{Workspaces: ws, Runner: runner}, started: time.Now(), logger: logger}
	if c.OpenAI.Enabled {
		s.openAI = openaiservice.New(c.OpenAI.APIKey, c.OpenAI.Model, c.OpenAI.MaxOutputTokens, time.Duration(c.OpenAI.TimeoutSeconds)*time.Second)
		s.openAIAuth = auth.NewHeaderPair(c.OpenAI.ClientKey, c.OpenAI.ClientSecret)
		s.openAILimiter = newRequestLimiter(c.OpenAI.RequestsPerMinute)
	}
	return s
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.home)
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("GET /assets/launch-banner.svg", s.launchBanner)
	mux.HandleFunc("GET /assets/openai-test.js", s.openAITestScript)
	mux.HandleFunc("GET /openai/test", s.openAITestPage)
	mux.HandleFunc("OPTIONS /openai/prompt", s.handleOpenAIPrompt)
	mux.HandleFunc("POST /openai/prompt", s.handleOpenAIPrompt)
	mux.HandleFunc("GET /downloads/{filename}", s.download)
	mux.Handle("GET /mcp", s.auth.Wrap(http.HandlerFunc(s.handleMCP)))
	mux.Handle("POST /mcp", s.auth.Wrap(http.HandlerFunc(s.handleMCP)))
	mux.Handle("DELETE /mcp", s.auth.Wrap(http.HandlerFunc(s.handleMCP)))
	return securityHeaders(mux)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		if r.URL.Path == "/openai/test" {
			w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'; img-src 'self' data:; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		} else {
			w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'none'; img-src 'self' data:; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	b, err := os.ReadFile(s.cfg.Server.Homepage)
	if err != nil {
		http.Error(w, "operator documentation unavailable", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write(b)
}
func (s *Server) launchBanner(w http.ResponseWriter, r *http.Request) {
	root, err := os.OpenRoot(filepath.Dir(s.cfg.Server.Homepage))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer root.Close()
	b, err := root.ReadFile("launch-banner.svg")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(b)
}
func (s *Server) download(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("filename")
	if _, ok := downloadableFiles[name]; !ok || filepath.Base(name) != name {
		http.NotFound(w, r)
		return
	}
	downloads := filepath.Join(filepath.Dir(s.cfg.Server.Homepage), "..", "downloads")
	root, err := os.OpenRoot(downloads)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer root.Close()
	f, err := root.Open(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
	w.Header().Set("Cache-Control", "public, max-age=300")
	http.ServeContent(w, r, name, st.ModTime(), f)
}
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"status": "ok", "service": "mcp", "version": Version, "transport": "Streamable HTTP (MCP 2026-07-28; compatible with 2025 revisions)", "uptime": time.Since(s.started).Round(time.Second).String()})
}

func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	if !s.validOrigin(r.Header.Get("Origin")) {
		writeJSON(w, 403, map[string]string{"error": "origin not allowed"})
		return
	}
	if r.Method == http.MethodGet {
		w.Header().Set("Allow", "POST")
		writeJSON(w, 405, map[string]string{"error": "this stateless server does not expose a general SSE listener"})
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST, GET")
		writeJSON(w, 405, map[string]string{"error": "method not allowed"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.Limits.MaxRequestBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var req rpcRequest
	if err := dec.Decode(&req); err != nil {
		s.rpc(w, nil, nil, -32700, "invalid JSON-RPC request", err.Error())
		return
	}
	if req.JSONRPC != "2.0" || req.Method == "" {
		s.rpc(w, req.ID, nil, -32600, "invalid request", nil)
		return
	}
	if err := validateMCPHeaders(r, &req); err != nil {
		s.rpc(w, req.ID, nil, -32600, err.Error(), nil)
		return
	}
	if len(req.ID) == 0 || string(req.ID) == "null" {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		pv := negotiate(p.ProtocolVersion)
		s.rpc(w, req.ID, map[string]any{"protocolVersion": pv, "capabilities": map[string]any{"tools": map[string]any{"listChanged": false}}, "serverInfo": map[string]string{"name": "remote-development-mcp", "version": Version}, "instructions": "Use small search and line-range reads before editing. Review git_diff before explicit commit, push, or deploy actions."}, 0, "", nil)
	case "ping":
		s.rpc(w, req.ID, map[string]any{}, 0, "", nil)
	case "tools/list":
		s.rpc(w, req.ID, map[string]any{"resultType": "complete", "tools": s.tools(), "ttlMs": 300000, "cacheScope": "private"}, 0, "", nil)
	case "tools/call":
		s.call(w, req)
	default:
		s.rpc(w, req.ID, nil, -32601, "method not found", nil)
	}
}

func (s *Server) call(w http.ResponseWriter, req rpcRequest) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil || p.Name == "" {
		s.rpc(w, req.ID, nil, -32602, "invalid tool call parameters", nil)
		return
	}
	if len(p.Arguments) == 0 {
		p.Arguments = []byte(`{}`)
	}
	start := time.Now()
	workspaceName, path := metadata(p.Arguments)
	value, cmdResult, err := s.dispatch(p.Name, p.Arguments)
	attrs := []any{"tool", p.Name, "workspace", workspaceName, "path", path, "duration_ms", time.Since(start).Milliseconds(), "success", err == nil}
	if isCommandTool(p.Name) {
		attrs = append(attrs, "command_category", p.Name)
	}
	s.logger.Info("mcp tool call", attrs...)
	result := map[string]any{"resultType": "complete", "isError": err != nil, "content": []map[string]string{{"type": "text"}}}
	if err != nil {
		result["content"].([]map[string]string)[0]["text"] = err.Error()
	} else {
		b, _ := json.MarshalIndent(value, "", "  ")
		result["content"].([]map[string]string)[0]["text"] = string(b)
		result["structuredContent"] = value
		if cmdResult != nil && cmdResult.ExitCode != 0 {
			result["isError"] = true
		}
	}
	s.rpc(w, req.ID, result, 0, "", nil)
}

func (s *Server) dispatch(name string, args json.RawMessage) (any, *command.Result, error) {
	decode := func(v any) error { return json.Unmarshal(args, v) }
	switch name {
	case "workspace_list":
		var out []map[string]any
		for _, w := range s.ws.List() {
			runs := make([]string, 0, len(w.RunCommands))
			for n := range w.RunCommands {
				runs = append(runs, n)
			}
			out = append(out, map[string]any{"name": w.Name, "read_only": w.ReadOnly, "build_configured": w.Build != nil, "test_configured": w.Test != nil, "deploy_configured": w.Deploy != nil, "run_commands": runs})
		}
		return map[string]any{"workspaces": out}, nil, nil
	case "tree":
		var a struct {
			Workspace     string `json:"workspace"`
			Path          string `json:"path"`
			Depth         int    `json:"depth"`
			MaxEntries    int    `json:"max_entries"`
			IncludeHidden bool   `json:"include_hidden"`
		}
		if e := decode(&a); e != nil {
			return nil, nil, e
		}
		v, t, e := s.search.Tree(a.Workspace, a.Path, a.Depth, a.MaxEntries, a.IncludeHidden)
		return map[string]any{"entries": v, "truncated": t}, nil, e
	case "file_find":
		var a struct {
			Workspace  string `json:"workspace"`
			Path       string `json:"path"`
			Pattern    string `json:"pattern"`
			MaxResults int    `json:"max_results"`
		}
		if e := decode(&a); e != nil {
			return nil, nil, e
		}
		v, t, e := s.search.FileFind(a.Workspace, a.Path, a.Pattern, a.MaxResults)
		return map[string]any{"files": v, "truncated": t}, nil, e
	case "code_search":
		var a struct {
			Workspace    string `json:"workspace"`
			Path         string `json:"path"`
			Pattern      string `json:"pattern"`
			Glob         string `json:"glob"`
			MaxResults   int    `json:"max_results"`
			ContextLines int    `json:"context_lines"`
		}
		if e := decode(&a); e != nil {
			return nil, nil, e
		}
		v, t, e := s.search.CodeSearch(a.Workspace, a.Path, a.Pattern, a.Glob, a.MaxResults, a.ContextLines)
		return map[string]any{"matches": v, "truncated": t}, nil, e
	case "read_file":
		var a struct {
			Workspace string `json:"workspace"`
			Path      string `json:"path"`
			StartLine int    `json:"start_line"`
			EndLine   int    `json:"end_line"`
		}
		if e := decode(&a); e != nil {
			return nil, nil, e
		}
		v, e := s.files.Read(a.Workspace, a.Path, a.StartLine, a.EndLine)
		return v, nil, e
	case "write_file":
		var a struct {
			Workspace      string `json:"workspace"`
			Path           string `json:"path"`
			Content        string `json:"content"`
			ExpectedSHA256 string `json:"expected_sha256"`
			Overwrite      bool   `json:"overwrite"`
		}
		if e := decode(&a); e != nil {
			return nil, nil, e
		}
		v, e := s.files.Write(a.Workspace, a.Path, a.Content, a.Overwrite, a.ExpectedSHA256)
		return v, nil, e
	case "apply_patch":
		var a struct {
			Workspace      string `json:"workspace"`
			Path           string `json:"path"`
			OldText        string `json:"old_text"`
			NewText        string `json:"new_text"`
			ExpectedSHA256 string `json:"expected_sha256"`
		}
		if e := decode(&a); e != nil {
			return nil, nil, e
		}
		v, e := s.files.ApplyPatch(a.Workspace, a.Path, a.OldText, a.NewText, a.ExpectedSHA256)
		return v, nil, e
	case "delete_file":
		var a struct {
			Workspace      string `json:"workspace"`
			Path           string `json:"path"`
			ExpectedSHA256 string `json:"expected_sha256"`
		}
		if e := decode(&a); e != nil {
			return nil, nil, e
		}
		v, e := s.files.Delete(a.Workspace, a.Path, a.ExpectedSHA256)
		return v, nil, e
	case "git_status":
		if !s.cfg.Commands.Git {
			return nil, nil, disabled("git")
		}
		a := workspaceArg(args)
		v, e := s.git.Status(a)
		return v, &v, e
	case "git_diff":
		if !s.cfg.Commands.Git {
			return nil, nil, disabled("git")
		}
		var a struct {
			Workspace, Path string
			Staged          bool
		}
		if e := decode(&a); e != nil {
			return nil, nil, e
		}
		v, e := s.git.Diff(a.Workspace, a.Staged, a.Path)
		return v, &v, e
	case "git_log":
		if !s.cfg.Commands.Git {
			return nil, nil, disabled("git")
		}
		var a struct {
			Workspace string
			Limit     int
		}
		if e := decode(&a); e != nil {
			return nil, nil, e
		}
		v, e := s.git.Log(a.Workspace, a.Limit)
		return v, &v, e
	case "git_fetch":
		if !s.cfg.Commands.Git {
			return nil, nil, disabled("git")
		}
		var a struct {
			Workspace      string `json:"workspace"`
			Remote         string `json:"remote"`
			TimeoutSeconds int    `json:"timeout_seconds"`
		}
		if e := decode(&a); e != nil {
			return nil, nil, e
		}
		v, e := s.git.Fetch(a.Workspace, a.Remote, a.TimeoutSeconds)
		return v, &v, e
	case "git_pull":
		if !s.cfg.Commands.Git {
			return nil, nil, disabled("git")
		}
		var a struct {
			Workspace      string `json:"workspace"`
			Remote         string `json:"remote"`
			Branch         string `json:"branch"`
			TimeoutSeconds int    `json:"timeout_seconds"`
		}
		if e := decode(&a); e != nil {
			return nil, nil, e
		}
		v, e := s.git.Pull(a.Workspace, a.Remote, a.Branch, a.TimeoutSeconds)
		return v, &v, e
	case "git_add":
		if !s.cfg.Commands.Git {
			return nil, nil, disabled("git")
		}
		var a struct {
			Workspace string
			Paths     []string
		}
		if e := decode(&a); e != nil {
			return nil, nil, e
		}
		v, e := s.git.Add(a.Workspace, a.Paths)
		return v, &v, e
	case "git_commit":
		if !s.cfg.Commands.Git {
			return nil, nil, disabled("git")
		}
		var a struct{ Workspace, Message string }
		if e := decode(&a); e != nil {
			return nil, nil, e
		}
		v, e := s.git.Commit(a.Workspace, a.Message)
		return v, &v, e
	case "git_push":
		if !s.cfg.Commands.Git {
			return nil, nil, disabled("git")
		}
		var a struct {
			Workspace      string `json:"workspace"`
			Remote         string `json:"remote"`
			Branch         string `json:"branch"`
			SetUpstream    bool   `json:"set_upstream"`
			TimeoutSeconds int    `json:"timeout_seconds"`
		}
		if e := decode(&a); e != nil {
			return nil, nil, e
		}
		v, e := s.git.Push(a.Workspace, a.Remote, a.Branch, a.SetUpstream, a.TimeoutSeconds)
		return v, &v, e
	case "build":
		return s.projectOperation(args, "build")
	case "test":
		return s.projectOperation(args, "test")
	case "run_command":
		if !s.cfg.Commands.Run {
			return nil, nil, disabled("run commands")
		}
		var a struct {
			Workspace      string `json:"workspace"`
			Command        string `json:"command"`
			TimeoutSeconds int    `json:"timeout_seconds"`
		}
		if e := decode(&a); e != nil {
			return nil, nil, e
		}
		w, e := s.ws.Get(a.Workspace)
		if e != nil {
			return nil, nil, e
		}
		if w.ReadOnly {
			return nil, nil, errors.New("workspace is read-only")
		}
		op, ok := w.RunCommands[a.Command]
		if !ok {
			return nil, nil, fmt.Errorf("unknown configured command %q", a.Command)
		}
		v, e := s.runner.Execute(a.Workspace, op, a.TimeoutSeconds)
		return v, &v, e
	case "deploy":
		if !s.cfg.Commands.Deploy {
			return nil, nil, disabled("deploy")
		}
		var a struct {
			Workspace      string `json:"workspace"`
			TimeoutSeconds int    `json:"timeout_seconds"`
		}
		if e := decode(&a); e != nil {
			return nil, nil, e
		}
		w, e := s.ws.Get(a.Workspace)
		if e != nil {
			return nil, nil, e
		}
		if w.ReadOnly {
			return nil, nil, errors.New("workspace is read-only")
		}
		if w.Deploy == nil {
			return nil, nil, errors.New("deployment is not configured for this workspace")
		}
		v, e := s.runner.Execute(a.Workspace, *w.Deploy, a.TimeoutSeconds)
		return v, &v, e
	default:
		return nil, nil, fmt.Errorf("unknown tool %q", name)
	}
}

func (s *Server) projectOperation(args json.RawMessage, kind string) (any, *command.Result, error) {
	enabled := s.cfg.Commands.Build
	if kind == "test" {
		enabled = s.cfg.Commands.Test
	}
	if !enabled {
		return nil, nil, disabled(kind)
	}
	var a struct {
		Workspace      string `json:"workspace"`
		Cwd            string `json:"cwd"`
		TimeoutSeconds int    `json:"timeout_seconds"`
	}
	if e := json.Unmarshal(args, &a); e != nil {
		return nil, nil, e
	}
	w, e := s.ws.Get(a.Workspace)
	if e != nil {
		return nil, nil, e
	}
	if w.ReadOnly {
		return nil, nil, errors.New("workspace is read-only")
	}
	op := w.Build
	if kind == "test" {
		op = w.Test
	}
	projectType := "configured"
	if op == nil {
		detected, typ, e := s.runner.Detect(a.Workspace, a.Cwd, kind)
		if e != nil {
			return map[string]any{"project_type": typ}, nil, e
		}
		op = &detected
		projectType = typ
	}
	v, e := s.runner.Execute(a.Workspace, *op, a.TimeoutSeconds)
	v.ProjectType = projectType
	return v, &v, e
}

func (s *Server) rpc(w http.ResponseWriter, id json.RawMessage, result any, code int, message string, data any) {
	if code != 0 {
		writeJSON(w, 200, rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: message, Data: data}})
		return
	}
	writeJSON(w, 200, rpcResponse{JSONRPC: "2.0", ID: id, Result: result})
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func (s *Server) validOrigin(origin string) bool {
	if origin == "" {
		return true
	}
	for _, v := range s.cfg.Server.AllowedOrigins {
		if origin == v {
			return true
		}
	}
	return false
}
func validateMCPHeaders(r *http.Request, req *rpcRequest) error {
	method := r.Header.Get("Mcp-Method")
	var envelope struct {
		Meta map[string]json.RawMessage `json:"_meta"`
	}
	_ = json.Unmarshal(req.Params, &envelope)
	var requestVersion string
	if raw := envelope.Meta["io.modelcontextprotocol/protocolVersion"]; len(raw) > 0 {
		_ = json.Unmarshal(raw, &requestVersion)
	}
	if requestVersion == LatestProtocol && method == "" {
		return errors.New("Mcp-Method header is required for MCP 2026-07-28")
	}
	if method != "" && method != req.Method {
		return errors.New("Mcp-Method header does not match JSON-RPC method")
	}
	if req.Method == "tools/call" {
		var p struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(req.Params, &p)
		name := r.Header.Get("Mcp-Name")
		if requestVersion == LatestProtocol && name == "" {
			return errors.New("Mcp-Name header is required for MCP 2026-07-28 tool calls")
		}
		if name != "" && name != p.Name {
			return errors.New("Mcp-Name header does not match tool name")
		}
	}
	return nil
}
func negotiate(v string) string {
	for _, x := range []string{LatestProtocol, "2025-11-25", "2025-06-18", "2025-03-26"} {
		if v == x {
			return x
		}
	}
	return LatestProtocol
}
func workspaceArg(b []byte) string {
	var a struct{ Workspace string }
	_ = json.Unmarshal(b, &a)
	return a.Workspace
}
func metadata(b []byte) (string, string) {
	var a struct{ Workspace, Path string }
	_ = json.Unmarshal(b, &a)
	return a.Workspace, a.Path
}
func disabled(v string) error {
	return fmt.Errorf("%s operations are disabled by server configuration", v)
}
func isCommandTool(n string) bool {
	return strings.HasPrefix(n, "git_") || n == "build" || n == "test" || n == "run_command" || n == "deploy"
}

func (s *Server) tools() []Tool {
	obj := func(props map[string]any, required ...string) map[string]any {
		return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
	}
	str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	integer := func(desc string, min, max int) map[string]any {
		return map[string]any{"type": "integer", "description": desc, "minimum": min, "maximum": max}
	}
	boolean := func(desc string) map[string]any { return map[string]any{"type": "boolean", "description": desc} }
	ws := str("Configured workspace name from workspace_list")
	ann := func(readOnly, destructive, open bool) map[string]any {
		return map[string]any{"readOnlyHint": readOnly, "destructiveHint": destructive, "openWorldHint": open}
	}
	return []Tool{
		{"workspace_list", "List workspaces", "List configured, enabled repository workspaces without exposing server filesystem paths.", obj(map[string]any{}), ann(true, false, false)},
		{"tree", "List tree", "List a bounded directory tree, excluding common dependency and sensitive paths.", obj(map[string]any{"workspace": ws, "path": str("Workspace-relative directory; defaults to ."), "depth": integer("Maximum traversal depth", 1, 20), "max_entries": integer("Maximum returned entries", 1, s.cfg.Limits.MaxSearchResult), "include_hidden": boolean("Include non-sensitive hidden files")}, "workspace"), ann(true, false, false)},
		{"file_find", "Find files", "Find filenames by case-insensitive substring or glob with a bounded result set.", obj(map[string]any{"workspace": ws, "path": str("Workspace-relative scope"), "pattern": str("Filename substring or glob"), "max_results": integer("Maximum matches", 1, s.cfg.Limits.MaxSearchResult)}, "workspace", "pattern"), ann(true, false, false)},
		{"code_search", "Search code", "Search repository contents with ripgrep and return filenames, line numbers, columns, and concise snippets.", obj(map[string]any{"workspace": ws, "path": str("Workspace-relative scope"), "pattern": str("Regular expression search pattern"), "glob": str("Optional ripgrep file glob, for example *.go"), "max_results": integer("Maximum match and context lines", 1, s.cfg.Limits.MaxSearchResult), "context_lines": integer("Context lines around matches", 0, 10)}, "workspace", "pattern"), ann(true, false, false)},
		{"read_file", "Read file range", "Read a UTF-8 text file or selected line range and return total lines plus a SHA-256 concurrency token.", obj(map[string]any{"workspace": ws, "path": str("Workspace-relative file path"), "start_line": integer("First 1-based line; defaults to 1", 1, 10000000), "end_line": integer("Last inclusive line; defaults to EOF", 1, 10000000)}, "workspace", "path"), ann(true, false, false)},
		{"write_file", "Create or replace file", "Create a text file. Replacing requires overwrite=true and the current expected_sha256.", obj(map[string]any{"workspace": ws, "path": str("Workspace-relative file path"), "content": str("Complete UTF-8 content"), "overwrite": boolean("Allow replacement"), "expected_sha256": str("Required current hash when replacing")}, "workspace", "path", "content"), ann(false, false, false)},
		{"apply_patch", "Apply targeted replacement", "Replace old_text only when it occurs exactly once; optionally require the current SHA-256. Returns a concise diff.", obj(map[string]any{"workspace": ws, "path": str("Workspace-relative file path"), "old_text": str("Exact existing text, including enough context to be unique"), "new_text": str("Replacement text"), "expected_sha256": str("Optional current file hash")}, "workspace", "path", "old_text", "new_text"), ann(false, false, false)},
		{"delete_file", "Delete file", "Delete one regular file only when expected_sha256 matches. Directories and protected paths are rejected.", obj(map[string]any{"workspace": ws, "path": str("Workspace-relative regular file"), "expected_sha256": str("Required current file hash")}, "workspace", "path", "expected_sha256"), ann(false, true, false)},
		{"git_status", "Git status", "Show concise branch and working tree status.", obj(map[string]any{"workspace": ws}, "workspace"), ann(true, false, false)},
		{"git_diff", "Git diff", "Show unstaged or staged changes, optionally scoped to a validated path.", obj(map[string]any{"workspace": ws, "staged": boolean("Show staged diff"), "path": str("Optional workspace-relative path")}, "workspace"), ann(true, false, false)},
		{"git_log", "Git log", "Show a bounded concise commit history.", obj(map[string]any{"workspace": ws, "limit": integer("Maximum commits", 1, 100)}, "workspace"), ann(true, false, false)},
		{"git_fetch", "Git fetch", "Fetch and prune remote references without changing checked-out files.", obj(map[string]any{"workspace": ws, "remote": str("Remote name; default fetches all"), "timeout_seconds": integer("Requested timeout capped at one hour", 1, 3600)}, "workspace"), ann(false, false, true)},
		{"git_pull", "Git pull", "Explicitly fast-forward pull; merge commits and force operations are prohibited.", obj(map[string]any{"workspace": ws, "remote": str("Optional remote"), "branch": str("Optional branch"), "timeout_seconds": integer("Timeout", 1, 3600)}, "workspace"), ann(false, false, true)},
		{"git_add", "Git add", "Explicitly stage validated workspace-relative paths.", obj(map[string]any{"workspace": ws, "paths": map[string]any{"type": "array", "items": str("Workspace-relative path"), "minItems": 1}}, "workspace", "paths"), ann(false, false, false)},
		{"git_commit", "Git commit", "Explicitly create a commit from already staged changes; never pushes.", obj(map[string]any{"workspace": ws, "message": str("Commit message")}, "workspace", "message"), ann(false, false, false)},
		{"git_push", "Git push", "Explicitly push without force; this is isolated from editing and committing.", obj(map[string]any{"workspace": ws, "remote": str("Remote name; defaults to origin"), "branch": str("Optional branch/refspec"), "set_upstream": boolean("Set upstream tracking"), "timeout_seconds": integer("Timeout", 1, 3600)}, "workspace"), ann(false, false, true)},
		{"build", "Build project", "Run an explicitly configured build or safely detect Go, npm, dotnet, make, or CMake.", obj(map[string]any{"workspace": ws, "cwd": str("Workspace-relative project directory"), "timeout_seconds": integer("Timeout", 1, 3600)}, "workspace"), ann(false, false, false)},
		{"test", "Test project", "Run an explicitly configured test command or safely detect the project type.", obj(map[string]any{"workspace": ws, "cwd": str("Workspace-relative project directory"), "timeout_seconds": integer("Timeout", 1, 3600)}, "workspace"), ann(false, false, false)},
		{"run_command", "Run approved project command", "Run one administrator-configured named command. Arbitrary shell strings are not accepted.", obj(map[string]any{"workspace": ws, "command": str("Configured command name"), "timeout_seconds": integer("Timeout", 1, 3600)}, "workspace", "command"), ann(false, false, false)},
		{"deploy", "Deploy workspace", "Explicitly run the workspace-specific administrator-configured deployment command.", obj(map[string]any{"workspace": ws, "timeout_seconds": integer("Timeout", 1, 3600)}, "workspace"), ann(false, true, true)},
	}
}

func HomepagePath(base string) string {
	if filepath.IsAbs(base) {
		return base
	}
	return filepath.Clean(base)
}
