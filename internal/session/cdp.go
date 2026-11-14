//go:build !js

package session

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// CDPCookieSource reads Euria cookies from a browser exposing a Chrome
// DevTools Protocol endpoint (e.g. started with --remote-debugging-port).
type CDPCookieSource struct {
	Endpoint string
}

// NewCDPCookieSource creates a source backed by a CDP endpoint.
func NewCDPCookieSource(endpoint string) *CDPCookieSource {
	return &CDPCookieSource{Endpoint: endpoint}
}

// Discover fetches Euria cookies from the CDP target.
func (s *CDPCookieSource) Discover(ctx context.Context, requested DiscoveryOptions) (DiscoveryResult, error) {
	if s.Endpoint == "" {
		return DiscoveryResult{}, fmt.Errorf("CDP endpoint is required")
	}
	allocatorCtx, cancel := chromedp.NewRemoteAllocator(ctx, s.Endpoint)
	defer cancel()
	browserCtx, cancel := chromedp.NewContext(allocatorCtx)
	defer cancel()

	var cookies []*network.Cookie
	err := chromedp.Run(browserCtx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			var err error
			cookies, err = network.GetCookies().WithURLs([]string{euriaOrigin}).Do(ctx)
			return err
		}),
	)
	if err != nil {
		return DiscoveryResult{}, fmt.Errorf("read cookies via CDP: %w", err)
	}

	now := time.Now()
	discovered := DiscoveryResult{}
	groups := make(map[string]int)
	for _, c := range cookies {
		converted := &http.Cookie{
			Name:     c.Name,
			Value:    c.Value,
			Domain:   c.Domain,
			Path:     c.Path,
			Secure:   c.Secure,
			HttpOnly: c.HTTPOnly,
		}
		if c.Expires > 0 {
			converted.Expires = time.Unix(int64(c.Expires), 0)
		}
		if !eligibleCookieFromHTTP(converted, now) {
			continue
		}
		// group by profile unknown -> single session
		key := "cdp"
		index, exists := groups[key]
		if !exists {
			index = len(discovered.Sessions)
			groups[key] = index
			discovered.Sessions = append(discovered.Sessions, Session{
				Profile: Profile{Browser: "cdp", Name: "remote", Path: s.Endpoint},
			})
		}
		discovered.Sessions[index].Cookies = append(discovered.Sessions[index].Cookies, converted)
		if converted.Name == "EURIA-API-XSRF-TOKEN" {
			discovered.Sessions[index].XsrfToken = converted.Value
		}
	}
	return discovered, nil
}

func eligibleCookieFromHTTP(cookie *http.Cookie, now time.Time) bool {
	if cookie == nil || cookie.Name == "" || cookie.Value == "" {
		return false
	}
	domain := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(cookie.Domain)), ".")
	if domain != "" && domain != "euria.infomaniak.com" && domain != "infomaniak.com" {
		return false
	}
	if cookie.Path == "" {
		cookie.Path = "/"
	}
	if cookie.Path != "/" {
		return false
	}
	if !cookie.Expires.IsZero() && !cookie.Expires.After(now) {
		return false
	}
	return true
}

var _ = network.Cookie{}
