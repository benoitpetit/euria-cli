//go:build darwin

package session

import "os/exec"

func openSystemURL(target string) error {
	process := exec.Command("open", target)
	if err := process.Start(); err != nil {
		return err
	}
	return process.Process.Release()
}
