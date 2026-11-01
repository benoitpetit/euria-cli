//go:build !linux

package session

// configureChromiumSafeStorage is a no-op on non-Linux platforms.
func configureChromiumSafeStorage(browser Browser) {}

func isChromiumBrowser(browser Browser) bool {
	switch browser {
	case "chrome", "chromium", "edge", "brave", "vivaldi", "opera", "arc", "helium",
		"dia", "comet", "atlas", "whale":
		return true
	default:
		return false
	}
}
