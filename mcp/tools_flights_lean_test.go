package mcp

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// `lean` is price discovery only: Google Flights and Kiwi, no savings pass, no
// composed split-ticket fares. An explicit `sources` still decides the providers
// and a default search is unchanged.
func TestSearchFlightsLeanAndSourcesReachTheSearch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	date := time.Now().AddDate(0, 1, 0).Format("2006-01-02")

	orig := dispatchFlightSearchFunc
	t.Cleanup(func() { dispatchFlightSearchFunc = orig })
	var got flights.SearchOptions
	dispatchFlightSearchFunc = func(_ context.Context, _ map[string]any, _, _, _ string, opts flights.SearchOptions) (*models.FlightSearchResult, error) {
		got = opts
		return &models.FlightSearchResult{Success: true}, nil
	}

	search := func(extra map[string]any) {
		t.Helper()
		params := map[string]any{"origin": "HEL", "destination": "CDG", "departure_date": date}
		for k, v := range extra {
			params[k] = v
		}
		got = flights.SearchOptions{}
		if _, _, isErr := callTravel(t, NewServer(), map[string]any{"intent": "search_flights", "params": params}); isErr {
			t.Fatal("search must not fail")
		}
	}

	search(nil)
	if len(got.Sources) != 0 || got.NoHacks || got.NoComposedFares {
		t.Fatalf("a default search must be unchanged, got %+v", got)
	}

	search(map[string]any{"lean": true})
	if want := []string{"google_flights", "kiwi"}; !reflect.DeepEqual(got.Sources, want) || !got.NoHacks || !got.NoComposedFares {
		t.Fatalf("lean must query Google Flights and Kiwi only, with no hacks and no composed fares, got %+v", got)
	}

	search(map[string]any{"lean": true, "sources": "Kiwi,skiplagged"})
	if want := []string{"kiwi", "skiplagged"}; !reflect.DeepEqual(got.Sources, want) || !got.NoHacks {
		t.Fatalf("an explicit sources must win over lean's default, got %+v", got)
	}

	search(map[string]any{"sources": []any{"google", "kiwi"}})
	if want := []string{"google_flights", "kiwi"}; !reflect.DeepEqual(got.Sources, want) || got.NoHacks || got.NoComposedFares {
		t.Fatalf("sources alone narrows the providers and nothing else, got %+v", got)
	}
}
