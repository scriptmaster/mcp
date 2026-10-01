package git

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"mcpdev/internal/command"
	"mcpdev/internal/config"
	"mcpdev/internal/workspace"
)

var safeRef = regexp.MustCompile(`^[A-Za-z0-9._/@+-]+$`)

func validRef(v string) bool {
	return safeRef.MatchString(v) && !strings.HasPrefix(v, "-") && !strings.HasPrefix(v, "+")
}

type Service struct {
	Workspaces *workspace.Manager
	Runner     *command.Runner
}

func (s *Service) Status(name string) (command.Result, error) {
	return s.run(name, []string{"git", "status", "--short", "--branch"}, 0)
}
func (s *Service) Diff(name string, staged bool, path string) (command.Result, error) {
	args := []string{"git", "diff", "--no-ext-diff"}
	if staged {
		args = append(args, "--cached")
	}
	if path != "" {
		if _, _, e := s.Workspaces.Resolve(name, path, false); e != nil {
			return command.Result{}, e
		}
		args = append(args, "--", path)
	}
	return s.run(name, args, 0)
}
func (s *Service) Log(name string, limit int) (command.Result, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	return s.run(name, []string{"git", "log", "--date=iso-strict", "--pretty=format:%h%x09%ad%x09%an%x09%s", "-n", strconv.Itoa(limit)}, 0)
}
func (s *Service) Fetch(name, remote string, timeout int) (command.Result, error) {
	if remote == "" {
		remote = "--all"
	}
	args := []string{"git", "fetch", "--prune"}
	if remote == "--all" {
		args = append(args, "--all")
	} else {
		if !validRef(remote) {
			return command.Result{}, errors.New("invalid remote")
		}
		args = append(args, remote)
	}
	return s.run(name, args, timeout)
}
func (s *Service) Pull(name, remote, branch string, timeout int) (command.Result, error) {
	args := []string{"git", "pull", "--ff-only"}
	for _, v := range []string{remote, branch} {
		if v != "" && !validRef(v) {
			return command.Result{}, errors.New("invalid remote or branch")
		}
	}
	if remote != "" {
		args = append(args, remote)
		if branch != "" {
			args = append(args, branch)
		}
	} else if branch != "" {
		return command.Result{}, errors.New("branch requires remote")
	}
	return s.run(name, args, timeout)
}
func (s *Service) Add(name string, paths []string) (command.Result, error) {
	if len(paths) == 0 {
		return command.Result{}, errors.New("at least one path is required")
	}
	args := []string{"git", "add", "--"}
	for _, p := range paths {
		if p == "." || p == "" {
			return command.Result{}, errors.New("git_add requires targeted file or directory paths; '.' is not allowed")
		}
		if _, _, e := s.Workspaces.Resolve(name, p, true); e != nil {
			return command.Result{}, fmt.Errorf("path %q: %w", p, e)
		}
		args = append(args, p)
	}
	return s.run(name, args, 0)
}
func (s *Service) Commit(name, message string) (command.Result, error) {
	message = strings.TrimSpace(message)
	if message == "" {
		return command.Result{}, errors.New("commit message is required")
	}
	if len(message) > 5000 {
		return command.Result{}, errors.New("commit message is too long")
	}
	return s.run(name, []string{"git", "commit", "-m", message}, 0)
}
func (s *Service) Push(name, remote, branch string, setUpstream bool, timeout int) (command.Result, error) {
	if remote == "" {
		remote = "origin"
	}
	for _, v := range []string{remote, branch} {
		if v != "" && !validRef(v) {
			return command.Result{}, errors.New("invalid remote or branch")
		}
	}
	args := []string{"git", "push"}
	if setUpstream {
		args = append(args, "--set-upstream")
	}
	args = append(args, remote)
	if branch != "" {
		args = append(args, branch)
	}
	return s.run(name, args, timeout)
}
func (s *Service) run(name string, args []string, timeout int) (command.Result, error) {
	if _, e := s.Workspaces.Get(name); e != nil {
		return command.Result{}, e
	}
	res, err := s.Runner.Execute(name, config.OperationConfig{Command: config.Command(args)}, timeout)
	if err != nil {
		return res, err
	}
	if res.ExitCode != 0 {
		return res, nil
	}
	return res, nil
}
