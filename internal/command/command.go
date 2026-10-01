package command

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"mcpdev/internal/config"
	"mcpdev/internal/workspace"
)

type Runner struct {
	Workspaces     *workspace.Manager
	MaxOutput      int
	DefaultTimeout time.Duration
}
type Result struct {
	Command         []string `json:"command"`
	Cwd             string   `json:"cwd"`
	Stdout          string   `json:"stdout"`
	Stderr          string   `json:"stderr"`
	ExitCode        int      `json:"exit_code"`
	DurationMS      int64    `json:"duration_ms"`
	TimedOut        bool     `json:"timed_out"`
	StdoutTruncated bool     `json:"stdout_truncated"`
	StderrTruncated bool     `json:"stderr_truncated"`
	ProjectType     string   `json:"project_type,omitempty"`
}

func (r *Runner) Execute(name string, op config.OperationConfig, requested int) (Result, error) {
	if len(op.Command) == 0 {
		return Result{}, errors.New("empty command")
	}
	_, cwd, err := r.Workspaces.Resolve(name, op.Cwd, false)
	if err != nil {
		return Result{}, err
	}
	st, err := os.Stat(cwd)
	if err != nil || !st.IsDir() {
		return Result{}, errors.New("command working directory is not a directory")
	}
	timeout := r.DefaultTimeout
	if op.Timeout > 0 {
		timeout = time.Duration(op.Timeout) * time.Second
	}
	if requested > 0 && time.Duration(requested)*time.Second < timeout {
		timeout = time.Duration(requested) * time.Second
	}
	if timeout > time.Hour {
		timeout = time.Hour
	}
	if timeout < time.Second {
		timeout = time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	// #nosec G204 -- commands are administrator-configured argv arrays; clients
	// select only a configured operation name and never supply a shell string.
	cmd := exec.CommandContext(ctx, op.Command[0], op.Command[1:]...)
	cmd.Dir = cwd
	cmd.Env = safeEnvironment()
	configureCommand(cmd)
	outBuf := &limitedBuffer{max: r.MaxOutput}
	errBuf := &limitedBuffer{max: r.MaxOutput}
	cmd.Stdout = outBuf
	cmd.Stderr = errBuf
	start := time.Now()
	err = cmd.Start()
	if err != nil {
		return Result{}, fmt.Errorf("start command: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timedOut := false
	select {
	case err = <-done:
	case <-ctx.Done():
		timedOut = true
		_ = terminateCommand(cmd)
		err = <-done
	}
	result := Result{Command: []string(op.Command), Cwd: relativeCwd(op.Cwd), Stdout: outBuf.String(), Stderr: errBuf.String(), ExitCode: 0, DurationMS: time.Since(start).Milliseconds(), TimedOut: timedOut, StdoutTruncated: outBuf.truncated, StderrTruncated: errBuf.truncated}
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			result.ExitCode = ee.ExitCode()
		} else {
			result.ExitCode = -1
		}
	}
	if timedOut {
		result.ExitCode = -1
		result.Stderr = strings.TrimSpace(result.Stderr) + "\ncommand timed out"
	}
	return result, nil
}

func (r *Runner) Detect(name, cwd, kind string) (config.OperationConfig, string, error) {
	_, root, err := r.Workspaces.Resolve(name, cwd, false)
	if err != nil {
		return config.OperationConfig{}, "", err
	}
	if exists(filepath.Join(root, "go.mod")) {
		if kind == "build" {
			return config.OperationConfig{Command: config.Command{"go", "build", "./..."}, Cwd: cwd}, "go", nil
		}
		return config.OperationConfig{Command: config.Command{"go", "test", "./..."}, Cwd: cwd}, "go", nil
	}
	if exists(filepath.Join(root, "package.json")) {
		script := kind
		if hasNPMScript(filepath.Join(root, "package.json"), script) {
			return config.OperationConfig{Command: config.Command{"npm", "run", script}, Cwd: cwd}, "npm", nil
		}
		return config.OperationConfig{}, "npm", fmt.Errorf("package.json has no %q script; configure it explicitly", script)
	}
	if exists(filepath.Join(root, "Makefile")) {
		target := kind
		return config.OperationConfig{Command: config.Command{"make", target}, Cwd: cwd}, "make", nil
	}
	if matches, _ := filepath.Glob(filepath.Join(root, "*.sln")); len(matches) > 0 {
		sort.Strings(matches)
		return config.OperationConfig{Command: config.Command{"dotnet", kind, filepath.Base(matches[0])}, Cwd: cwd}, "dotnet", nil
	}
	if matches, _ := filepath.Glob(filepath.Join(root, "*.csproj")); len(matches) > 0 {
		sort.Strings(matches)
		return config.OperationConfig{Command: config.Command{"dotnet", kind, filepath.Base(matches[0])}, Cwd: cwd}, "dotnet", nil
	}
	if exists(filepath.Join(root, "CMakeLists.txt")) {
		if kind == "build" && exists(filepath.Join(root, "build")) {
			return config.OperationConfig{Command: config.Command{"cmake", "--build", "build"}, Cwd: cwd}, "cmake", nil
		}
		return config.OperationConfig{}, "cmake", errors.New("CMake project detected; configure non-destructive build/test commands explicitly")
	}
	return config.OperationConfig{}, "unknown", errors.New("no supported project type detected; configure the operation explicitly")
}

func safeEnvironment() []string {
	return []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "HOME=/tmp/mcp-home", "GOCACHE=/tmp/mcp-gocache", "GOMODCACHE=/tmp/mcp-gomodcache", "LANG=C.UTF-8", "LC_ALL=C.UTF-8", "GIT_TERMINAL_PROMPT=0", "NPM_CONFIG_UPDATE_NOTIFIER=false"}
}
func exists(p string) bool { _, err := os.Stat(p); return err == nil }
func relativeCwd(c string) string {
	if c == "" {
		return "."
	}
	return filepath.ToSlash(c)
}
func hasNPMScript(path, name string) bool {
	// #nosec G304 -- path is produced by workspace.Manager.Resolve and is not a
	// raw client path. It is read only to inspect package.json script names.
	b, e := os.ReadFile(path)
	if e != nil {
		return false
	}
	var v struct {
		Scripts map[string]any `json:"scripts"`
	}
	return json.Unmarshal(b, &v) == nil && v.Scripts[name] != nil
}

type limitedBuffer struct {
	mu        sync.Mutex
	b         bytes.Buffer
	max       int
	truncated bool
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := len(p)
	remaining := l.max - l.b.Len()
	if remaining > 0 {
		if len(p) > remaining {
			_, _ = l.b.Write(p[:remaining])
			l.truncated = true
		} else {
			_, _ = l.b.Write(p)
		}
	} else {
		l.truncated = true
	}
	return n, nil
}
func (l *limitedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.b.String()
	if l.truncated {
		s += "\n... output truncated ...\n"
	}
	return s
}
