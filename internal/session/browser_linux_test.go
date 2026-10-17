//go:build linux

package session

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestChromiumBrowserLauncherUsesChromeProfile(t *testing.T) {
	var lookedUp []string
	var command string
	var args []string
	launcher := ChromiumBrowserLauncher{
		lookPath: func(name string) (string, error) {
			lookedUp = append(lookedUp, name)
			if name == "google-chrome-stable" {
				return "/usr/bin/google-chrome-stable", nil
			}
			return "", errors.New("not found")
		},
		flatpakInfo: func(string, string) error { return nil },
		startProcess: func(path string, gotArgs ...string) error {
			command = path
			args = append([]string(nil), gotArgs...)
			return nil
		},
	}
	profile := Profile{Browser: "chrome", Name: "Profile 2", Path: "/home/test/.config/google-chrome/Profile 2"}
	if err := launcher.Launch(profile, "https://euria.infomaniak.com/api/1/login"); err != nil {
		t.Fatalf("Launch() error = %v", err)
	}
	if !reflect.DeepEqual(lookedUp, []string{"google-chrome", "google-chrome-stable"}) {
		t.Fatalf("looked up = %#v, want Chrome executable candidates", lookedUp)
	}
	if command != "/usr/bin/google-chrome-stable" {
		t.Fatalf("command = %q, want stable Chrome executable", command)
	}
	wantArgs := []string{
		"--user-data-dir=/home/test/.config/google-chrome",
		"--profile-directory=Profile 2",
		"https://euria.infomaniak.com/api/1/login",
	}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", args, wantArgs)
	}
}

func TestChromiumBrowserLauncherUsesChromiumProfile(t *testing.T) {
	var command string
	var args []string
	launcher := ChromiumBrowserLauncher{
		lookPath: func(name string) (string, error) {
			if name == "chromium-browser" {
				return "/usr/bin/chromium-browser", nil
			}
			return "", errors.New("not found")
		},
		flatpakInfo: func(string, string) error { return nil },
		startProcess: func(path string, gotArgs ...string) error {
			command = path
			args = append([]string(nil), gotArgs...)
			return nil
		},
	}
	profile := Profile{Browser: "chromium", Name: "Default", Path: "/home/test/.config/chromium/Default/Network/Cookies"}
	if err := launcher.Launch(profile, "https://euria.infomaniak.com/api/1/login"); err != nil {
		t.Fatalf("Launch() error = %v", err)
	}
	if command != "/usr/bin/chromium-browser" {
		t.Fatalf("command = %q, want Chromium executable", command)
	}
	wantArgs := []string{
		"--user-data-dir=/home/test/.config/chromium",
		"--profile-directory=Default",
		"https://euria.infomaniak.com/api/1/login",
	}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", args, wantArgs)
	}
}

func TestChromiumBrowserLauncherUsesMatchingFlatpak(t *testing.T) {
	var command string
	var args []string
	launcher := ChromiumBrowserLauncher{
		lookPath: func(name string) (string, error) {
			if name == "flatpak" {
				return "/usr/bin/flatpak", nil
			}
			return "", errors.New("not found")
		},
		flatpakInfo: func(string, string) error { return nil },
		startProcess: func(path string, gotArgs ...string) error {
			command = path
			args = append([]string(nil), gotArgs...)
			return nil
		},
	}
	profile := Profile{Browser: "chrome", Name: "Default", Path: "/home/test/.var/app/com.google.Chrome/config/google-chrome/Default"}
	if err := launcher.Launch(profile, "https://euria.infomaniak.com/api/1/login"); err != nil {
		t.Fatalf("Launch() error = %v", err)
	}
	if command != "/usr/bin/flatpak" {
		t.Fatalf("command = %q, want Flatpak", command)
	}
	wantArgs := []string{
		"run", "com.google.Chrome",
		"--user-data-dir=/home/test/.var/app/com.google.Chrome/config/google-chrome",
		"--profile-directory=Default", "https://euria.infomaniak.com/api/1/login",
	}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", args, wantArgs)
	}
}

func TestChromiumBrowserLauncherRejectsMissingFlatpakApp(t *testing.T) {
	started := false
	infoChecked := false
	launcher := ChromiumBrowserLauncher{
		lookPath: func(name string) (string, error) {
			if name == "flatpak" {
				return "/usr/bin/flatpak", nil
			}
			return "", errors.New("not found")
		},
		flatpakInfo: func(path, appID string) error {
			infoChecked = path == "/usr/bin/flatpak" && appID == "com.google.Chrome"
			return errors.New("not installed")
		},
		startProcess: func(string, ...string) error {
			started = true
			return nil
		},
	}
	profile := Profile{Browser: "chrome", Name: "Default", Path: "/home/test/.var/app/com.google.Chrome/config/google-chrome/Default"}
	err := launcher.Launch(profile, "https://euria.infomaniak.com/api/1/login")
	if err == nil || !strings.Contains(err.Error(), "Flatpak Chrome app is not installed") || !infoChecked || started {
		t.Fatalf("Launch() error = %v, app check = %t, started = %t; want an actionable missing-app error before launch", err, infoChecked, started)
	}
}

func TestChromiumBrowserLauncherReportsMissingExecutable(t *testing.T) {
	launcher := ChromiumBrowserLauncher{
		lookPath:     func(string) (string, error) { return "", errors.New("not found") },
		startProcess: func(string, ...string) error { return nil },
	}
	err := launcher.Launch(Profile{Browser: "chromium", Path: "/home/test/chromium/Default"}, "https://euria.infomaniak.com/api/1/login")
	if err == nil || !strings.Contains(err.Error(), "install Chrome or Chromium") {
		t.Fatalf("Launch() error = %v, want actionable missing-browser guidance", err)
	}
}

func TestChromiumBrowserLauncherRejectsUnexpectedTarget(t *testing.T) {
	launcher := ChromiumBrowserLauncher{
		lookPath:     func(name string) (string, error) { return "/usr/bin/" + name, nil },
		startProcess: func(string, ...string) error { return nil },
	}
	err := launcher.Launch(Profile{Browser: "chrome", Path: "/home/test/chrome/Default"}, "https://example.com")
	if err == nil || !strings.Contains(err.Error(), "Euria HTTPS") {
		t.Fatalf("Launch() error = %v, want Euria destination restriction", err)
	}
}
