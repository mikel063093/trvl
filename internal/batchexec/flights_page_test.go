package batchexec

import (
	"context"
	"errors"
	"github.com/MikkoParkkola/trvl/internal/cache"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFlightPageRetryAfterSurvivesAnotherClient(t *testing.T) {
	hits := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(429)
	}))
	defer ts.Close()
	dir := t.TempDir()
	c := NewTestClient(ts.URL)
	c.refusalDir = dir
	status, _, err := c.SearchFlightsPage(context.Background(), "token", "US", "USD")
	if status != 429 || err != nil {
		t.Fatalf("status=%d err=%v", status, err)
	}
	until := readRefusalDeadline(t, dir)
	if remaining := time.Until(until); remaining < 59*time.Minute || remaining > 61*time.Minute {
		t.Fatalf("Retry-After clipped: %v", remaining)
	}
	other := NewTestClient(ts.URL)
	other.refusalDir = dir
	if _, _, err := other.SearchFlightsPage(context.Background(), "another", "US", "USD"); !errors.Is(err, ErrRefused) {
		t.Fatalf("err=%v", err)
	}
	if hits != 1 {
		t.Fatalf("calls = %d", hits)
	}
}

func TestFlightPageDoesNotCacheMissingOrMalformedData(t *testing.T) {
	for _, body := range []string{`<script>AF_initDataCallback({key: 'ds:0',data:[]});</script>`, `<script>AF_initDataCallback({key: 'ds:1',data:[broken]});</script>`} {
		hits := 0
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++; _, _ = w.Write([]byte(body)) }))
		c := NewTestClient(ts.URL)
		c.cache = cache.New()
		for range 2 {
			_, _, _ = c.SearchFlightsPage(context.Background(), "token", "US", "USD")
		}
		ts.Close()
		if hits != 2 {
			t.Fatalf("invalid page was cached: %d upstream calls", hits)
		}
	}
}
