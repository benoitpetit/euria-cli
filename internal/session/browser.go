package session

import (
	"errors"
	"fmt"
)

const EuriaLoginURL = "https://euria.infomaniak.com/api/1/login"

// BrowserLauncher opens Euria's sign-in page in the operating system browser.
type BrowserLauncher interface {
	Open(target string) error
}

type systemBrowserLauncher struct {
	openURL func(string) error
}

// NewSystemBrowserLauncher creates a launcher that delegates to the OS URL handler.
func NewSystemBrowserLauncher() BrowserLauncher {
	return systemBrowserLauncher{}
}

func (l systemBrowserLauncher) Open(target string) error {
	if target != EuriaLoginURL {
		return errors.New("browser launch target must be the Euria HTTPS sign-in endpoint")
	}
	openURL := l.openURL
	if openURL == nil {
		openURL = openSystemURL
	}
	if err := openURL(target); err != nil {
		return fmt.Errorf("could not open Euria sign-in in the system browser: %w", err)
	}
	return nil
}
