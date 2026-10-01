package command

import (
	"mcpdev/internal/config"
	"mcpdev/internal/workspace"
	"runtime"
	"strings"
	"testing"
	"time"
)

func runner(t *testing.T, max int) *Runner {
	root := t.TempDir()
	return &Runner{Workspaces: workspace.New([]config.WorkspaceConfig{{Name: "w", Path: root}}), MaxOutput: max, DefaultTimeout: time.Second}
}
func TestCommandTimeout(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip()
	}
	r := runner(t, 1024)
	res, err := r.Execute("w", config.OperationConfig{Command: config.Command{"sh", "-c", "sleep 5"}, Timeout: 1}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !res.TimedOut {
		t.Fatalf("expected timeout: %#v", res)
	}
}
func TestCommandOutputLimit(t *testing.T) {
	r := runner(t, 64)
	res, err := r.Execute("w", config.OperationConfig{Command: config.Command{"sh", "-c", "yes x | head -1000"}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !res.StdoutTruncated || !strings.Contains(res.Stdout, "truncated") {
		t.Fatalf("not truncated: %#v", res)
	}
}
