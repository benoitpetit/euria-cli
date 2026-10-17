//go:build windows

package session

import "os/exec"

func openSystemURL(target string) error {
	process := exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", target)
	if err := process.Start(); err != nil {
		return err
	}
	return process.Process.Release()
}
