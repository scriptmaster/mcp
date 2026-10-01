package search

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"mcpdev/internal/workspace"
)

type Service struct {
	Workspaces *workspace.Manager
	MaxResults int
}
type TreeEntry struct {
	Path string `json:"path"`
	Type string `json:"type"`
	Size int64  `json:"size,omitempty"`
}
type SearchMatch struct {
	Path   string `json:"path"`
	Line   int    `json:"line"`
	Column int    `json:"column,omitempty"`
	Kind   string `json:"kind"`
	Text   string `json:"text"`
}

func (s *Service) Tree(name, scope string, depth, max int, hidden bool) ([]TreeEntry, bool, error) {
	_, root, err := s.Workspaces.Resolve(name, scope, false)
	if err != nil {
		return nil, false, err
	}
	st, err := os.Stat(root)
	if err != nil {
		return nil, false, err
	}
	if !st.IsDir() {
		return nil, false, errors.New("tree path must be a directory")
	}
	if depth <= 0 {
		depth = 4
	}
	if depth > 20 {
		depth = 20
	}
	max = s.limit(max)
	baseDepth := strings.Count(filepath.Clean(root), string(filepath.Separator))
	out := make([]TreeEntry, 0)
	truncated := false
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return nil
		}
		if p == root {
			return nil
		}
		relRoot, _ := filepath.Rel(root, p)
		relWorkspace, _ := s.relative(name, p)
		level := strings.Count(filepath.Clean(p), string(filepath.Separator)) - baseDepth
		if level > depth {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		base := d.Name()
		if d.IsDir() && (base == ".git" || base == "node_modules" || base == "vendor") {
			return filepath.SkipDir
		}
		if !hidden && strings.HasPrefix(base, ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if workspace.SafeReadPath(relWorkspace) != nil {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if len(out) >= max {
			truncated = true
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, ie := d.Info()
		if ie != nil {
			return nil
		}
		typ := "file"
		if d.IsDir() {
			typ = "directory"
		} else if info.Mode()&os.ModeSymlink != 0 {
			typ = "symlink"
		}
		out = append(out, TreeEntry{Path: filepath.ToSlash(filepath.Join(scope, relRoot)), Type: typ, Size: info.Size()})
		return nil
	})
	return out, truncated, err
}

func (s *Service) FileFind(name, scope, pattern string, max int) ([]string, bool, error) {
	_, root, err := s.Workspaces.Resolve(name, scope, false)
	if err != nil {
		return nil, false, err
	}
	max = s.limit(max)
	pattern = strings.ToLower(pattern)
	var out []string
	truncated := false
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return nil
		}
		if p == root {
			return nil
		}
		base := d.Name()
		if d.IsDir() && (base == ".git" || base == "node_modules" || base == "vendor") {
			return filepath.SkipDir
		}
		rel, _ := s.relative(name, p)
		if workspace.SafeReadPath(rel) != nil {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		matched := strings.Contains(strings.ToLower(base), pattern)
		if strings.ContainsAny(pattern, "*?[") {
			matched, _ = filepath.Match(pattern, strings.ToLower(base))
			if !matched {
				matched, _ = filepath.Match(pattern, strings.ToLower(filepath.ToSlash(rel)))
			}
		}
		if matched {
			if len(out) >= max {
				truncated = true
				return nil
			}
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(out)
	return out, truncated, err
}

func (s *Service) CodeSearch(name, scope, pattern, glob string, max, contextLines int) ([]SearchMatch, bool, error) {
	if pattern == "" {
		return nil, false, errors.New("pattern is required")
	}
	if contextLines < 0 {
		contextLines = 0
	}
	if contextLines > 10 {
		contextLines = 10
	}
	max = s.limit(max)
	_, root, err := s.Workspaces.Resolve(name, scope, false)
	if err != nil {
		return nil, false, err
	}
	if rg, err := exec.LookPath("rg"); err == nil {
		return s.ripgrep(name, rg, root, pattern, glob, max, contextLines)
	}
	return s.fallback(name, root, pattern, glob, max)
}

func (s *Service) ripgrep(name, rg, root, pattern, glob string, max, contextLines int) ([]SearchMatch, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	args := []string{"--json", "--color=never", "--hidden", "--glob", "!.git/**", "--glob", "!node_modules/**", "--glob", "!vendor/**"}
	if glob != "" {
		args = append(args, "--glob", glob)
	}
	if contextLines > 0 {
		args = append(args, "--context", fmt.Sprint(contextLines))
	}
	args = append(args, "--", pattern, root)
	cmd := exec.CommandContext(ctx, rg, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, false, errors.New("search timed out")
	}
	if err != nil {
		if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 1 {
			return nil, false, fmt.Errorf("rg: %s", strings.TrimSpace(stderr.String()))
		}
	}
	var out []SearchMatch
	truncated := false
	scanner := bufio.NewScanner(&stdout)
	scanner.Buffer(make([]byte, 64<<10), 2<<20)
	for scanner.Scan() {
		var ev struct {
			Type string `json:"type"`
			Data struct {
				Path struct {
					Text string `json:"text"`
				} `json:"path"`
				Lines struct {
					Text string `json:"text"`
				} `json:"lines"`
				LineNumber int `json:"line_number"`
				Submatches []struct {
					Start int `json:"start"`
				} `json:"submatches"`
			} `json:"data"`
		}
		if json.Unmarshal(scanner.Bytes(), &ev) != nil {
			continue
		}
		if ev.Type != "match" && ev.Type != "context" {
			continue
		}
		rel, _ := s.relative(name, ev.Data.Path.Text)
		if workspace.SafeReadPath(rel) != nil {
			continue
		}
		if len(out) >= max {
			truncated = true
			break
		}
		col := 0
		if len(ev.Data.Submatches) > 0 {
			col = ev.Data.Submatches[0].Start + 1
		}
		out = append(out, SearchMatch{Path: filepath.ToSlash(rel), Line: ev.Data.LineNumber, Column: col, Kind: ev.Type, Text: strings.TrimRight(ev.Data.Lines.Text, "\r\n")})
	}
	return out, truncated, scanner.Err()
}

func (s *Service) fallback(name, root, pattern, glob string, max int) ([]SearchMatch, bool, error) {
	w, err := s.Workspaces.Get(name)
	if err != nil {
		return nil, false, err
	}
	rootFS, err := os.OpenRoot(w.Path)
	if err != nil {
		return nil, false, err
	}
	defer rootFS.Close()
	var out []SearchMatch
	truncated := false
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "node_modules" || d.Name() == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := s.relative(name, p)
		if workspace.SafeReadPath(rel) != nil {
			return nil
		}
		if glob != "" {
			ok, _ := filepath.Match(glob, filepath.Base(p))
			if !ok {
				return nil
			}
		}
		f, e := rootFS.Open(filepath.FromSlash(rel))
		if e != nil {
			return nil
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		line := 0
		for sc.Scan() {
			line++
			txt := sc.Text()
			if strings.Contains(txt, pattern) {
				if len(out) >= max {
					truncated = true
					return nil
				}
				out = append(out, SearchMatch{Path: filepath.ToSlash(rel), Line: line, Kind: "match", Text: txt})
			}
		}
		return nil
	})
	return out, truncated, err
}

func (s *Service) relative(name, p string) (string, error) {
	w, err := s.Workspaces.Get(name)
	if err != nil {
		return "", err
	}
	r, err := filepath.Rel(w.Path, p)
	return filepath.ToSlash(r), err
}
func (s *Service) limit(n int) int {
	if n <= 0 {
		n = 50
	}
	if n > s.MaxResults {
		n = s.MaxResults
	}
	return n
}
