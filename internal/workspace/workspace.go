package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"mcpdev/internal/config"
)

var ErrEscape = errors.New("path escapes workspace")
var ErrProtected = errors.New("path is protected")

type Manager struct {
	items map[string]config.WorkspaceConfig
}

func New(items []config.WorkspaceConfig) *Manager {
	m := &Manager{items: make(map[string]config.WorkspaceConfig)}
	for _, w := range items {
		if w.IsEnabled() {
			m.items[w.Name] = w
		}
	}
	return m
}

func (m *Manager) List() []config.WorkspaceConfig {
	out := make([]config.WorkspaceConfig, 0, len(m.items))
	for _, w := range m.items {
		out = append(out, w)
	}
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].Name < out[i].Name {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func (m *Manager) Get(name string) (config.WorkspaceConfig, error) {
	w, ok := m.items[name]
	if !ok {
		return config.WorkspaceConfig{}, fmt.Errorf("unknown or disabled workspace %q", name)
	}
	return w, nil
}

// Resolve validates a workspace-relative path and resolves every existing
// symlink component. Nonexistent leaf components are allowed for file creation.
func (m *Manager) Resolve(name, rel string, write bool) (config.WorkspaceConfig, string, error) {
	w, err := m.Get(name)
	if err != nil {
		return w, "", err
	}
	if write && w.ReadOnly {
		return w, "", errors.New("workspace is read-only")
	}
	if rel == "" {
		rel = "."
	}
	if filepath.IsAbs(rel) || filepath.VolumeName(rel) != "" {
		return w, "", ErrEscape
	}
	rel = filepath.Clean(filepath.FromSlash(rel))
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return w, "", ErrEscape
	}
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == ".." {
			return w, "", ErrEscape
		}
	}
	if write && protected(w, rel) {
		return w, "", ErrProtected
	}
	candidate := filepath.Join(w.Path, rel)
	resolved, err := resolveExisting(candidate)
	if err != nil {
		return w, "", err
	}
	if !inside(w.Path, resolved) {
		return w, "", ErrEscape
	}
	return w, resolved, nil
}

func resolveExisting(path string) (string, error) {
	cur := filepath.Clean(path)
	var suffix []string
	for {
		resolved, err := filepath.EvalSymlinks(cur)
		if err == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return filepath.Clean(resolved), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", err
		}
		suffix = append(suffix, filepath.Base(cur))
		cur = parent
	}
}

func inside(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func protected(w config.WorkspaceConfig, rel string) bool {
	rel = filepath.ToSlash(filepath.Clean(rel))
	parts := strings.Split(rel, "/")
	for _, p := range parts {
		low := strings.ToLower(p)
		if low == ".git" || low == ".env" || strings.HasPrefix(low, ".env.") || low == "id_rsa" || low == "id_ed25519" || strings.HasSuffix(low, ".pem") || strings.HasSuffix(low, ".key") {
			return true
		}
	}
	for _, p := range w.ProtectedPaths {
		p = filepath.ToSlash(filepath.Clean(p))
		if rel == p || strings.HasPrefix(rel, strings.TrimSuffix(p, "/")+"/") {
			return true
		}
	}
	return false
}

func SafeReadPath(rel string) error {
	rel = filepath.ToSlash(filepath.Clean(rel))
	for _, p := range strings.Split(rel, "/") {
		low := strings.ToLower(p)
		if low == ".env" || strings.HasPrefix(low, ".env.") || low == "id_rsa" || low == "id_ed25519" || low == "authorized_keys" || low == "credentials" || strings.HasSuffix(low, ".pem") || strings.HasSuffix(low, ".key") || strings.Contains(low, "secret") {
			return ErrProtected
		}
	}
	return nil
}
