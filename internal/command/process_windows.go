//go:build windows

package command

import "os/exec"

func configureCommand(_ *exec.Cmd) {}

func terminateCommand(cmd *exec.Cmd) error {
	return cmd.Process.Kill()
}
