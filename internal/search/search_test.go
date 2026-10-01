package search

import (
	"mcpdev/internal/config"
	"mcpdev/internal/workspace"
	"os"
	"path/filepath"
	"testing"
)

func TestSearchResultLimit(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "a.txt"), []byte("needle\nneedle\nneedle\n"), 0600)
	s := &Service{Workspaces: workspace.New([]config.WorkspaceConfig{{Name: "w", Path: root}}), MaxResults: 10}
	r, truncated, err := s.CodeSearch("w", ".", "needle", "", 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(r) != 2 || !truncated {
		t.Fatalf("len=%d truncated=%v", len(r), truncated)
	}
}
