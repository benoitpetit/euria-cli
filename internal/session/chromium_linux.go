//go:build linux

package session

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// isChromiumBrowser reports whether browser uses Chromium's OS keyring-backed
// Safe Storage password.
func isChromiumBrowser(browser Browser) bool {
	switch browser {
	case "chrome", "chromium", "edge", "brave", "vivaldi", "opera", "arc", "helium",
		"dia", "comet", "atlas", "whale":
		return true
	default:
		return false
	}
}

// chromiumEnvKey returns the Sweetcookie environment variable used to override
// the Safe Storage password for browser.
func chromiumEnvKey(browser Browser) string {
	switch browser {
	case "chrome":
		return "GOOKIE_CHROME_SAFE_STORAGE_PASSWORD"
	case "chromium":
		return "GOOKIE_CHROMIUM_SAFE_STORAGE_PASSWORD"
	case "edge":
		return "GOOKIE_EDGE_SAFE_STORAGE_PASSWORD"
	case "brave":
		return "GOOKIE_BRAVE_SAFE_STORAGE_PASSWORD"
	case "vivaldi":
		return "GOOKIE_VIVALDI_SAFE_STORAGE_PASSWORD"
	case "opera":
		return "GOOKIE_OPERA_SAFE_STORAGE_PASSWORD"
	case "arc":
		return "GOOKIE_ARC_SAFE_STORAGE_PASSWORD"
	case "helium":
		return "GOOKIE_HELIUM_SAFE_STORAGE_PASSWORD"
	case "dia":
		return "GOOKIE_DIA_SAFE_STORAGE_PASSWORD"
	case "comet":
		return "GOOKIE_COMET_SAFE_STORAGE_PASSWORD"
	case "atlas":
		return "GOOKIE_ATLAS_SAFE_STORAGE_PASSWORD"
	case "whale":
		return "GOOKIE_WHALE_SAFE_STORAGE_PASSWORD"
	default:
		return "GOOKIE_SAFE_STORAGE_PASSWORD"
	}
}

// chromiumApplicationNames returns likely Secret Service application attribute
// values for browser.
func chromiumApplicationNames(browser Browser) []string {
	switch browser {
	case "chrome":
		return []string{"chrome", "google-chrome", "google chrome"}
	case "chromium":
		return []string{"chromium", "chromium-browser"}
	case "edge":
		return []string{"microsoft-edge", "msedge", "edge"}
	case "brave":
		return []string{"brave", "brave-browser"}
	case "vivaldi":
		return []string{"vivaldi"}
	case "opera":
		return []string{"opera"}
	case "arc":
		return []string{"arc"}
	case "helium":
		return []string{"helium"}
	case "dia":
		return []string{"dia"}
	case "comet":
		return []string{"comet"}
	case "atlas":
		return []string{"atlas"}
	case "whale":
		return []string{"whale"}
	default:
		return []string{string(browser)}
	}
}

// chromiumLegacyServiceAccount returns the legacy service/account pair used by
// older Chrome builds.
func chromiumLegacyServiceAccount(browser Browser) (string, string) {
	switch browser {
	case "chrome":
		return "Chrome Safe Storage", "Chrome"
	case "chromium":
		return "Chromium Safe Storage", "Chromium"
	case "edge":
		return "Microsoft Edge Safe Storage", "Microsoft Edge"
	case "brave":
		return "Brave Safe Storage", "Brave"
	case "vivaldi":
		return "Vivaldi Safe Storage", "Vivaldi"
	case "opera":
		return "Opera Safe Storage", "Opera"
	case "arc":
		return "Arc Safe Storage", "Arc"
	case "helium":
		return "Helium Storage Key", "Helium"
	case "dia":
		return "Dia Safe Storage", "Dia"
	case "comet":
		return "Comet Safe Storage", "Comet"
	case "atlas":
		return "ChatGPT Safe Storage", "ChatGPT"
	case "whale":
		return "Whale Safe Storage", "Whale"
	default:
		label := string(browser)
		return label + " Safe Storage", label
	}
}

// linuxChromiumSafeStoragePassword resolves a Chromium Safe Storage password from
// the Linux desktop keyring. It prefers Secret Service application attributes,
// then legacy service/account attributes, then KWallet.
func linuxChromiumSafeStoragePassword(browser Browser) (string, error) {
	// Secret Service application attributes.
	for _, application := range chromiumApplicationNames(browser) {
		if password, err := secretToolLookup("application", application); err == nil && strings.TrimSpace(password) != "" {
			return strings.TrimSpace(password), nil
		}
	}

	// Legacy Secret Service service/account attributes.
	service, account := chromiumLegacyServiceAccount(browser)
	if password, err := secretToolLookup("service", service, "account", account); err == nil && strings.TrimSpace(password) != "" {
		return strings.TrimSpace(password), nil
	}

	// KWallet fallback.
	if password, err := kwalletLookup(service); err == nil && strings.TrimSpace(password) != "" {
		return strings.TrimSpace(password), nil
	}

	return "", fmt.Errorf("could not read %s Safe Storage password from the Linux keyring", browser)
}

func secretToolLookup(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "secret-tool", append([]string{"lookup"}, args...)...)
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(output), nil
}

func kwalletLookup(service string) (string, error) {
	// Best-effort; mirrors Sweetcookie's KWallet probing without extra D-Bus
	// service discovery.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "kwallet-query", "--read-password", service, "--folder", service+" Keys", "kdewallet")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	result := strings.TrimSpace(string(output))
	if strings.HasPrefix(strings.ToLower(result), "failed to read") {
		return "", fmt.Errorf("kwallet-query failed")
	}
	return result, nil
}

// configureChromiumSafeStorage sets the Sweetcookie override environment
// variable when a keyring password can be resolved.
func configureChromiumSafeStorage(browser Browser) {
	if !isChromiumBrowser(browser) {
		return
	}
	if existing := os.Getenv(chromiumEnvKey(browser)); strings.TrimSpace(existing) != "" {
		return
	}
	password, err := linuxChromiumSafeStoragePassword(browser)
	if err != nil || strings.TrimSpace(password) == "" {
		return
	}
	_ = os.Setenv(chromiumEnvKey(browser), strings.TrimSpace(password))
}
