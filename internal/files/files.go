package files

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"mcpdev/internal/workspace"
)

type Service struct {
	Workspaces        *workspace.Manager
	MaxRead, MaxWrite int
}

type ReadResult struct {
	Path       string `json:"path"`
	StartLine  int    `json:"start_line"`
	EndLine    int    `json:"end_line"`
	TotalLines int    `json:"total_lines"`
	Truncated  bool   `json:"truncated"`
	SHA256     string `json:"sha256"`
	Content    string `json:"content"`
}

type ChangeResult struct {
	Path    string `json:"path"`
	SHA256  string `json:"sha256,omitempty"`
	Diff    string `json:"diff,omitempty"`
	Deleted bool   `json:"deleted,omitempty"`
}

func (s *Service) Read(name, rel string, start, end int) (ReadResult, error) {
	if err := workspace.SafeReadPath(rel); err != nil {
		return ReadResult{}, err
	}
	_, p, err := s.Workspaces.Resolve(name, rel, false)
	if err != nil {
		return ReadResult{}, err
	}
	st, err := os.Lstat(p)
	if err != nil {
		return ReadResult{}, err
	}
	if !st.Mode().IsRegular() {
		return ReadResult{}, errors.New("path is not a regular file")
	}
	if st.Size() > int64(s.MaxRead) {
		return ReadResult{}, fmt.Errorf("file is %d bytes; limit is %d; use code_search or a narrower source file", st.Size(), s.MaxRead)
	}
	// #nosec G304 -- p is constrained by workspace.Manager.Resolve, including
	// absolute-path, traversal, protected-name, and symlink-escape checks.
	b, err := os.ReadFile(p)
	if err != nil {
		return ReadResult{}, err
	}
	if strings.IndexByte(string(b), 0) >= 0 {
		return ReadResult{}, errors.New("binary files are not readable through read_file")
	}
	lines := strings.Split(string(b), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	total := len(lines)
	if start <= 0 {
		start = 1
	}
	if end <= 0 || end > total {
		end = total
	}
	if start > end+1 || start > total+1 {
		return ReadResult{}, fmt.Errorf("start_line %d is beyond file length %d", start, total)
	}
	selected := ""
	if start <= end && total > 0 {
		selected = strings.Join(lines[start-1:end], "\n")
		if end < total || len(b) > 0 && b[len(b)-1] == '\n' {
			selected += "\n"
		}
	}
	h := sha256.Sum256(b)
	return ReadResult{Path: filepath.ToSlash(rel), StartLine: start, EndLine: end, TotalLines: total, Truncated: start > 1 || end < total, SHA256: hex.EncodeToString(h[:]), Content: selected}, nil
}

func (s *Service) Write(name, rel, content string, overwrite bool, expected string) (ChangeResult, error) {
	if len(content) > s.MaxWrite {
		return ChangeResult{}, fmt.Errorf("content exceeds %d byte limit", s.MaxWrite)
	}
	_, p, err := s.Workspaces.Resolve(name, rel, true)
	if err != nil {
		return ChangeResult{}, err
	}
	var old []byte
	st, statErr := os.Lstat(p)
	if statErr == nil {
		if !st.Mode().IsRegular() {
			return ChangeResult{}, errors.New("existing path is not a regular file")
		}
		if !overwrite {
			return ChangeResult{}, errors.New("file exists; set overwrite=true and provide expected_sha256")
		}
		// #nosec G304 -- p is constrained by workspace.Manager.Resolve.
		old, err = os.ReadFile(p)
		if err != nil {
			return ChangeResult{}, err
		}
		if expected == "" || hash(old) != expected {
			return ChangeResult{}, errors.New("expected_sha256 is required and must match current content")
		}
	} else if !os.IsNotExist(statErr) {
		return ChangeResult{}, statErr
	}
	if statErr != nil && overwrite {
		return ChangeResult{}, errors.New("cannot overwrite a file that does not exist")
	}
	if err := os.MkdirAll(filepath.Dir(p), 0750); err != nil {
		return ChangeResult{}, err
	}
	mode := os.FileMode(0640)
	if statErr == nil {
		mode = st.Mode().Perm()
	}
	if err := atomicWrite(p, []byte(content), mode); err != nil {
		return ChangeResult{}, err
	}
	return ChangeResult{Path: filepath.ToSlash(rel), SHA256: hash([]byte(content)), Diff: conciseDiff(rel, string(old), content)}, nil
}

func (s *Service) ApplyPatch(name, rel, oldText, newText, expected string) (ChangeResult, error) {
	if oldText == "" {
		return ChangeResult{}, errors.New("old_text must not be empty")
	}
	_, p, err := s.Workspaces.Resolve(name, rel, true)
	if err != nil {
		return ChangeResult{}, err
	}
	// #nosec G304 -- p is constrained by workspace.Manager.Resolve.
	b, err := os.ReadFile(p)
	if err != nil {
		return ChangeResult{}, err
	}
	if len(b) > s.MaxWrite {
		return ChangeResult{}, fmt.Errorf("file exceeds %d byte patch limit", s.MaxWrite)
	}
	if expected != "" && hash(b) != expected {
		return ChangeResult{}, errors.New("expected_sha256 does not match current content")
	}
	count := strings.Count(string(b), oldText)
	if count != 1 {
		return ChangeResult{}, fmt.Errorf("old_text must match exactly once; found %d matches", count)
	}
	updated := strings.Replace(string(b), oldText, newText, 1)
	if len(updated) > s.MaxWrite {
		return ChangeResult{}, fmt.Errorf("patched file exceeds %d byte limit", s.MaxWrite)
	}
	st, err := os.Stat(p)
	if err != nil {
		return ChangeResult{}, err
	}
	if err := atomicWrite(p, []byte(updated), st.Mode().Perm()); err != nil {
		return ChangeResult{}, err
	}
	return ChangeResult{Path: filepath.ToSlash(rel), SHA256: hash([]byte(updated)), Diff: conciseDiff(rel, string(b), updated)}, nil
}

func (s *Service) Delete(name, rel, expected string) (ChangeResult, error) {
	if expected == "" {
		return ChangeResult{}, errors.New("expected_sha256 is required for deletion")
	}
	_, p, err := s.Workspaces.Resolve(name, rel, true)
	if err != nil {
		return ChangeResult{}, err
	}
	st, err := os.Lstat(p)
	if err != nil {
		return ChangeResult{}, err
	}
	if !st.Mode().IsRegular() {
		return ChangeResult{}, errors.New("delete_file only deletes regular files")
	}
	// #nosec G304 -- p is constrained by workspace.Manager.Resolve.
	b, err := os.ReadFile(p)
	if err != nil {
		return ChangeResult{}, err
	}
	if hash(b) != expected {
		return ChangeResult{}, errors.New("expected_sha256 does not match current content")
	}
	if err := os.Remove(p); err != nil {
		return ChangeResult{}, err
	}
	return ChangeResult{Path: filepath.ToSlash(rel), Diff: conciseDiff(rel, string(b), ""), Deleted: true}, nil
}

func atomicWrite(path string, b []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".mcp-write-*")
	if err != nil {
		return err
	}
	name := f.Name()
	ok := false
	defer func() {
		_ = f.Close()
		if !ok {
			_ = os.Remove(name)
		}
	}()
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(name, path)
	}
	if err != nil {
		return err
	}
	ok = true
	return nil
}

func hash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func conciseDiff(path, old, new string) string {
	const capBytes = 32 << 10
	var b strings.Builder
	fmt.Fprintf(&b, "--- a/%s\n+++ b/%s\n", filepath.ToSlash(path), filepath.ToSlash(path))
	oldLines, newLines := strings.Split(old, "\n"), strings.Split(new, "\n")
	prefix := 0
	for prefix < len(oldLines) && prefix < len(newLines) && oldLines[prefix] == newLines[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(oldLines)-prefix && suffix < len(newLines)-prefix && oldLines[len(oldLines)-1-suffix] == newLines[len(newLines)-1-suffix] {
		suffix++
	}
	ctxStart := prefix - 2
	if ctxStart < 0 {
		ctxStart = 0
	}
	oldEnd := len(oldLines) - suffix + 2
	if oldEnd > len(oldLines) {
		oldEnd = len(oldLines)
	}
	newEnd := len(newLines) - suffix + 2
	if newEnd > len(newLines) {
		newEnd = len(newLines)
	}
	fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", ctxStart+1, oldEnd-ctxStart, ctxStart+1, newEnd-ctxStart)
	for i := ctxStart; i < prefix; i++ {
		b.WriteString(" " + oldLines[i] + "\n")
	}
	for i := prefix; i < len(oldLines)-suffix; i++ {
		b.WriteString("-" + oldLines[i] + "\n")
	}
	for i := prefix; i < len(newLines)-suffix; i++ {
		b.WriteString("+" + newLines[i] + "\n")
	}
	commonEnd := oldEnd - (len(oldLines) - suffix)
	for i := 0; i < commonEnd; i++ {
		b.WriteString(" " + oldLines[len(oldLines)-suffix+i] + "\n")
	}
	if b.Len() > capBytes {
		return b.String()[:capBytes] + "\n... diff truncated ...\n"
	}
	return b.String()
}

func CopyLimited(dst io.Writer, src io.Reader, max int) (int64, bool, error) {
	n, err := io.CopyN(dst, src, int64(max)+1)
	if err != nil && !errors.Is(err, io.EOF) {
		return n, false, err
	}
	return n, n > int64(max), nil
}
