package batchexec

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// SearchFlightsPage uses ordinary Go HTTP/TLS, no impersonation, cookies,
// browser challenge token, CAPTCHA handling, or fallback to a refused RPC.
func (c *Client) SearchFlightsPage(ctx context.Context, tfs, gl, curr string) (int, []byte, error) {
	q := url.Values{"tfs": {tfs}, "hl": {"en"}}
	if gl != "" {
		q.Set("gl", gl)
	}
	if curr != "" {
		q.Set("curr", curr)
	}
	endpoint := "https://www.google.com/travel/flights?" + q.Encode()
	if body, ok := c.getCached(endpoint, ""); ok {
		return 200, body, nil
	}
	status, body, err := c.doWithRetryVia(ctx, c.pageHTTP, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err == nil {
			req.Header.Set("Accept", "text/html")
			req.Header.Set("Accept-Language", "en")
			req.Header.Set("User-Agent", chromeUA+" trvl/1.27.0")
		}
		return req, err
	})
	// Cache only pages with the expected data callback, never an interstitial.
	if err == nil && status == 200 && !PageRefusal(body) {
		if _, decodeErr := DecodeFlightPage(body); decodeErr == nil {
			c.cacheSuccess(endpoint, "", status, body, FlightCacheTTL)
		}
	}
	return status, body, err
}

var flightPageData = regexp.MustCompile(`(?s)AF_initDataCallback\(\{\s*key:\s*['"]ds:1['"].*?\bdata:\s*`)

func DecodeFlightPage(body []byte) (any, error) {
	loc := flightPageData.FindIndex(body)
	if loc == nil {
		return nil, fmt.Errorf("google search page missing ds:1")
	}
	var payload []any
	if err := json.NewDecoder(strings.NewReader(string(body[loc[1]:]))).Decode(&payload); err != nil {
		return nil, fmt.Errorf("google search page invalid ds:1: %w", err)
	}
	if len(payload) < 4 {
		return nil, fmt.Errorf("google search page truncated ds:1")
	}
	return payload, nil
}

// PageRefusal recognizes explicit challenge/consent pages without executing
// their scripts. Such a response is a stop, never a prompt to change identity.
func PageRefusal(body []byte) bool {
	text := strings.ToLower(string(body))
	if strings.Contains(text, "af_initdatacallback") && (strings.Contains(text, "key: 'ds:1'") || strings.Contains(text, `key: "ds:1"`)) {
		return false
	}
	for _, marker := range []string{"/sorry/", "unusual traffic", "g-recaptcha", "consent.google.com", "before you continue to google"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}
