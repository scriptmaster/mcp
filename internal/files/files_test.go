package files

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mcpdev/internal/config"
	"mcpdev/internal/workspace"
)

func service(t *testing.T) (*Service, string) {
	t.Helper()
	root := t.TempDir()
	m := workspace.New([]config.WorkspaceConfig{{Name: "w", Path: root}})
	return &Service{Workspaces: m, MaxRead: 1024, MaxWrite: 1024}, root
}

func TestReadLineRanges(t *testing.T) {
	s, root := service(t)
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("one\ntwo\nthree\nfour\n"), 0600); err != nil {
		t.Fatal(err)
	}
	r, err := s.Read("w", "a.txt", 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	if r.Content != "two\nthree\n" || r.TotalLines != 4 || !r.Truncated {
		t.Fatalf("bad result: %#v", r)
	}
}

func TestPatchOperation(t *testing.T) {
	s, root := service(t)
	p := filepath.Join(root, "a.txt")
	_ = os.WriteFile(p, []byte("alpha\nbeta\n"), 0600)
	r, err := s.ApplyPatch("w", "a.txt", "beta", "gamma", "")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "alpha\ngamma\n" || !strings.Contains(r.Diff, "-beta") {
		t.Fatalf("patch failed: %s %#v", b, r)
	}
	if _, err = s.ApplyPatch("w", "a.txt", "missing", "x", ""); err == nil {
		t.Fatal("expected no-match error")
	}
}
