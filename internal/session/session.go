package session

import (
	"context"
	"net/http"
)

// Browser identifies a browser cookie source supported by Sweetcookie.
type Browser string

// Profile identifies one browser profile and its cookie store.
type Profile struct {
	Browser Browser
	Name    string
	Path    string
}

// Session contains Euria cookies loaded from a single browser profile.
type Session struct {
	Profile   Profile
	Cookies   []*http.Cookie
	XsrfToken string
}

// DiscoveryOptions selects browser sources and optional per-browser profiles.
type DiscoveryOptions struct {
	Browsers []Browser
	Profiles map[Browser]string
}

// DiscoveryResult holds available sessions and non-fatal source diagnostics.
type DiscoveryResult struct {
	Sessions []Session
	Warnings []string
}

// CookieSource reads Euria sessions without making network requests.
type CookieSource interface {
	Discover(context.Context, DiscoveryOptions) (DiscoveryResult, error)
}
