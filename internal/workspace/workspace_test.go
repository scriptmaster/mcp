package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"mcpdev/internal/config"
)

func TestResolveIsolation(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	m := New([]config.WorkspaceConfig{{Name: "test", Path: root}})
	if _, p, err := m.Resolve("test", "ok/new.txt", true); err != nil || p != filepath.Join(root, "ok/new.txt") {
		t.Fatalf("valid path: %q %v", p, err)
	}
	for _, bad := range []string{"../outside", "/etc/shadow", "a/../../outside", "escape/file"} {
		if _, _, err := m.Resolve("test", bad, false); !errors.Is(err, ErrEscape) {
			t.Errorf("%q: got %v", bad, err)
		}
	}
}

func TestWorkspaceValidation(t *testing.T) {
	m := New([]config.WorkspaceConfig{{Name: "enabled", Path: t.TempDir()}, {Name: "disabled", Path: t.TempDir(), Enabled: boolp(false)}})
	if _, err := m.Get("enabled"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Get("disabled"); err == nil {
		t.Fatal("disabled workspace was exposed")
	}
}

func boolp(v bool) *bool { return &v }
