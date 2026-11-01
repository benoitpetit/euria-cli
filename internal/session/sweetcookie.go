package session

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/steipete/sweetcookie"
)

const euriaOrigin = "https://euria.infomaniak.com/"

type getCookiesFunc func(context.Context, sweetcookie.Options) (sweetcookie.Result, error)

// SweetcookieCookieSource adapts Sweetcookie's browser results to Euria sessions.
type SweetcookieCookieSource struct {
	getCookies getCookiesFunc
}

// NewSweetcookieCookieSource creates a source that reads cookies from local browsers.
func NewSweetcookieCookieSource() *SweetcookieCookieSource {
	return &SweetcookieCookieSource{getCookies: sweetcookie.Get}
}

// Discover reads eligible Euria cookies from the requested browser profiles.
func (s *SweetcookieCookieSource) Discover(ctx context.Context, requested DiscoveryOptions) (DiscoveryResult, error) {
	if err := ctx.Err(); err != nil {
		return DiscoveryResult{}, err
	}
	getCookies := s.getCookies
	if getCookies == nil {
		getCookies = sweetcookie.Get
	}
	browsers := requested.Browsers
	if len(browsers) == 0 {
		defaults := sweetcookie.DefaultBrowsers()
		browsers = make([]Browser, len(defaults))
		for index, browser := range defaults {
			browsers[index] = Browser(browser)
		}
	}
	loaded := sweetcookie.Result{}
	for _, browser := range browsers {
		if err := ctx.Err(); err != nil {
			return DiscoveryResult{}, err
		}
		configureChromiumSafeStorage(browser)
		profile := requested.Profiles[browser]
		if profile == "" {
			profile = defaultProfile(browser)
		}
		options := sweetcookie.Options{
			URL:      euriaOrigin,
			Mode:     sweetcookie.ModeMerge,
			Browsers: []sweetcookie.Browser{sweetcookie.Browser(browser)},
		}
		if profile != "" {
			options.Profiles = map[sweetcookie.Browser]string{sweetcookie.Browser(browser): profile}
		}
		result, err := getCookies(ctx, options)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return DiscoveryResult{}, ctxErr
			}
			loaded.Warnings = append(loaded.Warnings, fmt.Sprintf("could not read %s cookies for Euria; check browser access and unlock the system keyring", browser))
			continue
		}
		loaded.Cookies = append(loaded.Cookies, result.Cookies...)
		loaded.Warnings = append(loaded.Warnings, result.Warnings...)
	}
	return groupCookies(loaded, time.Now()), nil
}

// Sweetcookie v0.0.2 deduplicates across profiles without source metadata, so
// automatic discovery uses one stable default profile per browser.
func defaultProfile(browser Browser) string {
	switch browser {
	case "chrome", "chromium", "edge", "brave", "vivaldi", "opera", "arc", "helium", "dia", "comet", "atlas", "whale":
		return "Default"
	case "firefox", "zen", "floorp", "waterfox", "librewolf":
		return "default-release"
	default:
		return ""
	}
}

func groupCookies(result sweetcookie.Result, now time.Time) DiscoveryResult {
	type sourceKey struct {
		browser   Browser
		profile   string
		storePath string
	}
	groups := make(map[sourceKey]int)
	discovered := DiscoveryResult{}
	values := make([]string, 0, len(result.Cookies))
	containerCookies := false
	for _, cookie := range result.Cookies {
		if cookie.Value != "" {
			values = append(values, cookie.Value)
		}
		if cookie.Container.ID != 0 {
			containerCookies = true
			continue
		}
		converted, ok := eligibleCookie(cookie, now)
		if !ok {
			continue
		}
		key := sourceKey{Browser(cookie.Source.Browser), cookie.Source.Profile, cookie.Source.StorePath}
		index, exists := groups[key]
		if !exists {
			index = len(discovered.Sessions)
			groups[key] = index
			discovered.Sessions = append(discovered.Sessions, Session{
				Profile: Profile{Browser: key.browser, Name: key.profile, Path: key.storePath},
			})
		}
		discovered.Sessions[index].Cookies = append(discovered.Sessions[index].Cookies, converted)
		if converted.Name == "EURIA-API-XSRF-TOKEN" {
			discovered.Sessions[index].XsrfToken = converted.Value
		}
	}
	discovered.Warnings = redactWarnings(result.Warnings, values)
	if containerCookies {
		discovered.Warnings = append(discovered.Warnings, "Firefox account-container cookies are not supported; sign in in the default container or use another browser")
	}
	return discovered
}

func eligibleCookie(cookie sweetcookie.Cookie, now time.Time) (*http.Cookie, bool) {
	if cookie.Name == "" || cookie.Value == "" {
		return nil, false
	}
	domain := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(cookie.Domain)), ".")
	if domain != "" && domain != "euria.infomaniak.com" && domain != "infomaniak.com" {
		return nil, false
	}
	path := cookie.Path
	if path == "" {
		path = "/"
	}
	if path != "/" || (cookie.Expires != nil && !cookie.Expires.After(now)) {
		return nil, false
	}
	converted := &http.Cookie{
		Name:     cookie.Name,
		Value:    cookie.Value,
		Domain:   cookie.Domain,
		Path:     path,
		Secure:   cookie.Secure,
		HttpOnly: cookie.HTTPOnly,
		SameSite: convertSameSite(cookie.SameSite),
	}
	if cookie.Expires != nil {
		converted.Expires = *cookie.Expires
	}
	return converted, true
}

func convertSameSite(value sweetcookie.SameSite) http.SameSite {
	switch value {
	case sweetcookie.SameSiteNone:
		return http.SameSiteNoneMode
	case sweetcookie.SameSiteLax:
		return http.SameSiteLaxMode
	case sweetcookie.SameSiteStrict:
		return http.SameSiteStrictMode
	default:
		return http.SameSiteDefaultMode
	}
}

func redactWarnings(warnings, cookieValues []string) []string {
	if len(warnings) == 0 {
		return nil
	}
	redacted := make([]string, len(warnings))
	for index, warning := range warnings {
		for _, value := range cookieValues {
			if value != "" {
				warning = strings.ReplaceAll(warning, value, "[redacted]")
			}
		}
		redacted[index] = warning
	}
	return redacted
}
