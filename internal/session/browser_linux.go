//go:build linux

package session

import "os/exec"

func openSystemURL(target string) error {
	process := exec.Command("xdg-open", target)
	if err := process.Start(); err != nil {
		return err
	}
	return process.Process.Release()
}
