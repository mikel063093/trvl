package flights

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/flights/afklm"
)

func TestNormalizeSources(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"nothing means every provider", nil, []string{}},
		{"blank entries are dropped", []string{"", "  "}, []string{}},
		{"case, spaces, duplicates and order", []string{" Kiwi", "KIWI", "google_flights"}, []string{"google_flights", "kiwi"}},
		{"aliases resolve to the ids the statuses use", []string{"google", "Wizz", "af-klm"}, []string{"afklm", "google_flights", "wizzair"}},
		{"a comma list in one entry", []string{"kiwi, skiplagged"}, []string{"kiwi", "skiplagged"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := NormalizeSources(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("NormalizeSources(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestSourceAllowed(t *testing.T) {
	if !(SearchOptions{}).sourceAllowed(SourceSkiplagged) {
		t.Fatal("no Sources must allow every provider (the unchanged default)")
	}
	opts := SearchOptions{Sources: NormalizeSources([]string{"google_flights", "kiwi"})}
	if !opts.sourceAllowed(SourceKiwi) {
		t.Fatal("kiwi is in the requested sources")
	}
	for _, id := range []string{SourceSkiplagged, SourceWizzair, SourceAFKLM} {
		if opts.sourceAllowed(id) {
			t.Fatalf("%s is not in the requested sources", id)
		}
	}
}

// A provider the caller left out is reported as skipped and never queried: the
// runners return before they look at the client, so a nil client proves it.
func TestProviderRunnersSkipSourcesNotRequested(t *testing.T) {
	opts := SearchOptions{Sources: NormalizeSources([]string{"google_flights"})}
	ctx := context.Background()
	runners := map[string]providerOutcome{
		"kiwi":       runKiwiProvider(ctx, nil, "BOG", "MAD", "2026-11-17", "COP", opts),
		"skiplagged": runSkiplaggedProvider(ctx, nil, "BOG", "MAD", "2026-11-17", opts),
		"ryanair":    runRyanairProvider(ctx, nil, "BOG", "MAD", "2026-11-17", "COP", opts),
		"wizzair":    runWizzairProvider(ctx, nil, "BOG", "MAD", "2026-11-17", "COP", opts),
		"transavia":  runTransaviaProvider(ctx, nil, "BOG", "MAD", "2026-11-17", "COP", opts),
		"easyjet":    runEasyjetProvider(ctx, nil, "BOG", "MAD", "2026-11-17", "COP", opts),
		"vueling":    runVuelingProvider(ctx, nil, "BOG", "MAD", "2026-11-17", "COP", opts),
		"norwegian":  runNorwegianProvider(ctx, nil, "BOG", "MAD", "2026-11-17", "COP", opts),
	}
	for id, out := range runners {
		if out.status.ID != id || out.status.Status != "skipped" || out.status.Error != "not in the requested sources" {
			t.Errorf("%s: status %+v, want skipped as not in the requested sources", id, out.status)
		}
		if out.succeeded || out.err != nil || len(out.flights) != 0 {
			t.Errorf("%s: outcome %+v, want nothing queried", id, out)
		}
	}
}

func TestNativeRoundTripsSkipSourcesNotRequested(t *testing.T) {
	opts := SearchOptions{
		Sources:          NormalizeSources([]string{"google_flights"}),
		afklmNewProvider: func() (*afklm.AFKLMProvider, error) { t.Fatal("AFKLM must not be built"); return nil, nil },
	}
	ctx := context.Background()
	shared := batchexec.SharedClient()
	if f, st := searchNativeRoundTrip(ctx, shared, "BOG", "MAD", "2026-11-17", "2026-11-24", opts, "COP"); f != nil || st != nil {
		t.Errorf("native Skiplagged round trip ran: %v %v", f, st)
	}
	if f, st := searchKiwiNativeRoundTrip(ctx, shared, "BOG", "MAD", "2026-11-17", "2026-11-24", opts, "COP"); f != nil || st != nil {
		t.Errorf("native Kiwi round trip ran: %v %v", f, st)
	}
	if f, st := searchAFKLMNativeRoundTrip(ctx, "BOG", "MAD", "2026-11-17", "2026-11-24", opts); f != nil || st != nil {
		t.Errorf("native AFKLM round trip ran: %v %v", f, st)
	}
}

// The outbound one-way search only feeds the composed (split-ticket) pairs:
// NoComposedFares leaves it out, and with it the composer status, while the
// native fare and the inbound search that gives it its return leg still run.
func TestSearchRoundTrip_NoComposedFares_SkipsTheOutboundLeg(t *testing.T) {
	body := makeTestFlightBody(t)
	run := func(opts SearchOptions) (calls int32, statusIDs []string) {
		var n int32
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&n, 1)
			w.WriteHeader(200)
			_, _ = w.Write(body)
		}))
		defer ts.Close()
		opts.ReturnDate = "2026-06-22"
		opts.afklmNewProvider = func() (*afklm.AFKLMProvider, error) { return nil, afklm.ErrNoCredential }
		res, err := SearchFlightsWithClient(context.Background(), batchexec.NewTestClient(ts.URL), "HEL", "NRT", "2026-06-15", opts)
		if err != nil {
			t.Fatalf("round trip failed: %v", err)
		}
		for _, st := range res.ProviderStatuses {
			statusIDs = append(statusIDs, st.ID)
		}
		return atomic.LoadInt32(&n), statusIDs
	}

	defaultCalls, defaultStatuses := run(SearchOptions{})
	leanCalls, leanStatuses := run(SearchOptions{NoComposedFares: true})

	if leanCalls != defaultCalls-1 {
		t.Fatalf("Google requests: default %d, NoComposedFares %d, want exactly one fewer", defaultCalls, leanCalls)
	}
	has := func(ids []string, id string) bool {
		for _, s := range ids {
			if s == id {
				return true
			}
		}
		return false
	}
	if !has(defaultStatuses, "roundtrip_composer") {
		t.Fatalf("default statuses %v must include the composer", defaultStatuses)
	}
	if has(leanStatuses, "roundtrip_composer") {
		t.Fatalf("NoComposedFares statuses %v must not include the composer", leanStatuses)
	}
}

func TestFlightSearchKey_SeparatesSourcesComposedFaresAndHacks(t *testing.T) {
	base := flightSearchKey("BOG", "MAD", "2026-11-17", SearchOptions{})
	for name, opts := range map[string]SearchOptions{
		"sources":           {Sources: []string{"kiwi"}},
		"no composed fares": {NoComposedFares: true},
		"no hacks":          {NoHacks: true},
	} {
		if flightSearchKey("BOG", "MAD", "2026-11-17", opts) == base {
			t.Errorf("%s must not share a singleflight/cache key with the default search", name)
		}
	}
	a := flightSearchKey("BOG", "MAD", "2026-11-17", SearchOptions{Sources: []string{"kiwi", "google_flights"}})
	b := flightSearchKey("BOG", "MAD", "2026-11-17", SearchOptions{Sources: []string{"google_flights", "kiwi"}})
	if a != b {
		t.Error("the same sources in another order must share a key")
	}
}
