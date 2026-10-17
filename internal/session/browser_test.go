package session

import (
	"errors"
	"testing"
)

func TestSystemBrowserLauncherOpensOnlyEuriaSignIn(t *testing.T) {
	var opened string
	launcher := systemBrowserLauncher{openURL: func(target string) error {
		opened = target
		return nil
	}}
	if err := launcher.Open(EuriaLoginURL); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if opened != EuriaLoginURL {
		t.Fatalf("opened URL = %q, want %q", opened, EuriaLoginURL)
	}
}

func TestSystemBrowserLauncherRejectsOtherURLs(t *testing.T) {
	called := false
	launcher := systemBrowserLauncher{openURL: func(string) error {
		called = true
		return nil
	}}
	if err := launcher.Open("https://example.com/"); err == nil {
		t.Fatal("Open() accepted a URL outside the Euria sign-in endpoint")
	}
	if called {
		t.Fatal("Open() invoked the system URL handler for an unsupported target")
	}
}

func TestSystemBrowserLauncherReportsOpenerDetails(t *testing.T) {
	launcher := systemBrowserLauncher{openURL: func(string) error { return errors.New("process details") }}
	if err := launcher.Open(EuriaLoginURL); err == nil || err.Error() != "could not open Euria sign-in in the system browser: process details" {
		t.Fatalf("Open() error = %v, want an actionable opener error", err)
	}
}
