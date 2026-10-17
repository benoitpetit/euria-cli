//go:build linux

package session

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// BrowserLauncher opens a URL in the selected Chrome/Chromium profile.
type BrowserLauncher interface {
	Launch(profile Profile, target string) error
}

// ChromiumBrowserLauncher starts Chrome or Chromium directly, without a shell.
type ChromiumBrowserLauncher struct {
	lookPath     func(string) (string, error)
	flatpakInfo  func(string, string) error
	startProcess func(string, ...string) error
}

// NewChromiumBrowserLauncher creates a launcher that uses the system PATH.
func NewChromiumBrowserLauncher() *ChromiumBrowserLauncher {
	return &ChromiumBrowserLauncher{
		lookPath: exec.LookPath,
		flatpakInfo: func(path, appID string) error {
			return exec.Command(path, "info", appID).Run()
		},
		startProcess: func(path string, args ...string) error {
			process := exec.Command(path, args...)
			if err := process.Start(); err != nil {
				return err
			}
			return process.Process.Release()
		},
	}
}

// Launch opens Euria in the selected browser profile and leaves the browser running.
func (l *ChromiumBrowserLauncher) Launch(profile Profile, target string) error {
	if target != "https://euria.infomaniak.com/api/1/login" {
		return errors.New("browser launch target must be the Euria HTTPS sign-in endpoint")
	}
	if profile.Browser != "chrome" && profile.Browser != "chromium" {
		return fmt.Errorf("unsupported browser profile %q", profile.Browser)
	}
	if profile.Path == "" {
		return errors.New("Chrome/Chromium profile path is empty")
	}
	lookPath := l.lookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	startProcess := l.startProcess
	if startProcess == nil {
		startProcess = NewChromiumBrowserLauncher().startProcess
	}

	userDataDir, profileName, err := launchProfileParts(profile.Path)
	if err != nil {
		return err
	}
	if appID, flatpakRoot, ok := flatpakProfileRoot(profile); ok {
		path, err := lookPath("flatpak")
		if err != nil {
			return errors.New("this profile uses Flatpak; install Flatpak and the matching Chrome/Chromium app to continue")
		}
		checkApp := l.flatpakInfo
		if checkApp == nil {
			checkApp = func(path, appID string) error {
				return exec.Command(path, "info", appID).Run()
			}
		}
		if err := checkApp(path, appID); err != nil {
			browser := "Chrome"
			if profile.Browser == "chromium" {
				browser = "Chromium"
			}
			return fmt.Errorf("Flatpak %s app is not installed; install it or select another profile: %w", browser, err)
		}
		args := []string{"run", appID, "--user-data-dir=" + flatpakRoot, "--profile-directory=" + profileName, target}
		if err := startProcess(path, args...); err != nil {
			return fmt.Errorf("could not launch the selected Flatpak browser: %w", err)
		}
		return nil
	}

	candidates := []string{"google-chrome", "google-chrome-stable"}
	expected := "Chrome"
	if profile.Browser == "chromium" {
		candidates = []string{"chromium", "chromium-browser"}
		expected = "Chromium"
	}
	var path string
	for _, candidate := range candidates {
		path, err = lookPath(candidate)
		if err == nil {
			break
		}
	}
	if err != nil {
		return fmt.Errorf("could not find %s; install Chrome or Chromium and retry", expected)
	}
	args := []string{
		"--user-data-dir=" + userDataDir,
		"--profile-directory=" + profileName,
		target,
	}
	if err := startProcess(path, args...); err != nil {
		return fmt.Errorf("could not launch %s: %w", expected, err)
	}
	return nil
}

func launchProfileParts(path string) (string, string, error) {
	profileDir := filepath.Clean(path)
	if filepath.Base(profileDir) == "Cookies" {
		profileDir = filepath.Dir(profileDir)
		if filepath.Base(profileDir) == "Network" {
			profileDir = filepath.Dir(profileDir)
		}
	}
	profileName := filepath.Base(profileDir)
	userDataDir := filepath.Dir(profileDir)
	if profileName == "." || profileName == string(filepath.Separator) || userDataDir == profileDir {
		return "", "", errors.New("Chrome/Chromium profile path is invalid")
	}
	return userDataDir, profileName, nil
}

func flatpakProfileRoot(profile Profile) (string, string, bool) {
	appID, configDir := "", ""
	if profile.Browser == "chrome" {
		appID, configDir = "com.google.Chrome", "google-chrome"
	} else if profile.Browser == "chromium" {
		appID, configDir = "org.chromium.Chromium", "chromium"
	} else {
		return "", "", false
	}

	path := filepath.Clean(profile.Path)
	if filepath.Base(path) == "Cookies" {
		path = filepath.Dir(path)
		if filepath.Base(path) == "Network" {
			path = filepath.Dir(path)
		}
	}
	parts := strings.Split(path, string(filepath.Separator))
	for index := 0; index+5 < len(parts); index++ {
		if parts[index] == ".var" && parts[index+1] == "app" && parts[index+2] == appID && parts[index+3] == "config" && parts[index+4] == configDir {
			return appID, filepath.Join(string(filepath.Separator), filepath.Join(parts[:index+5]...)), true
		}
	}
	return "", "", false
}
