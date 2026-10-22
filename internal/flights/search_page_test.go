package flights

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/models"
)

func TestGoogleSearchUsesOrdinaryPage(t *testing.T) {
	body, err := os.ReadFile("testdata/google_flights_page.html")
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/travel/flights" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("tfs") != "CBwQAhogEgoyMDI2LTEwLTI1KABqBwgBEgNKRktyBwgBEgNMSFJAAUgBcAGYAQI" {
			t.Errorf("tfs = %s", q.Get("tfs"))
		}
		if q.Get("curr") != "USD" || q.Get("hl") != "en" {
			t.Errorf("locale = %v", q)
		}
		if r.Header.Get("x-goog-batchexecute-bgr") != "" {
			t.Error("unexpected browser challenge token")
		}
		if !strings.Contains(r.UserAgent(), "trvl/") {
			t.Errorf("client must identify itself honestly: %q", r.UserAgent())
		}
		_, _ = w.Write(body)
	}))
	defer ts.Close()
	got, err := searchGoogleFlightsWithClient(context.Background(), batchexec.NewTestClient(ts.URL), "JFK", "LHR", "2026-10-25", SearchOptions{Adults: 1, Currency: "USD", CabinClass: models.Economy, MaxStops: models.NonStop})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Success || len(got.Flights) != 20 || got.Flights[0].Price != 295 {
		t.Fatalf("result = %+v", got)
	}
	for _, f := range got.Flights {
		if len(f.Legs) == 0 || f.Price <= 0 || f.Currency != "USD" {
			t.Fatalf("flight = %+v", f)
		}
	}
}

func TestGooglePageRoundTripUsesSelectedOutboundReturnPrice(t *testing.T) {
	body, err := os.ReadFile("testdata/google_flights_page.html")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := batchexec.DecodeFlightPage(body)
	if err != nil {
		t.Fatal(err)
	}
	p := payload.([]any)
	entry := p[2].([]any)[0].([]any)[0].([]any)
	p[2] = []any{[]any{entry}}
	p[3] = []any{[]any{}}
	raw, _ := json.Marshal(p)
	outbound := fmt.Sprintf(`<script>AF_initDataCallback({key: 'ds:1',data:%s, sideChannel:{}});</script>`, raw)
	info := entry[0].([]any)
	leg := info[2].([]any)[0].([]any)
	leg[3] = "LHR"
	leg[6] = "JFK"
	leg[20] = []int{2026, 11, 1}
	leg[21] = []int{2026, 11, 1}
	leg[22] = []any{"B6", "777", nil, "JetBlue"}
	entry[1].([]any)[0] = []any{nil, 700}
	raw, _ = json.Marshal(p)
	inbound := fmt.Sprintf(`<script>AF_initDataCallback({key: 'ds:1',data:%s, sideChannel:{}});</script>`, raw)
	hits := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		token, _ := base64.RawURLEncoding.DecodeString(r.URL.Query().Get("tfs"))
		if hits == 1 {
			_, _ = w.Write([]byte(outbound))
			return
		}
		if !strings.Contains(string(token), "1107") {
			t.Error("return query did not pin outbound flight B61107")
		}
		_, _ = w.Write([]byte(inbound))
	}))
	defer ts.Close()
	got, statuses := searchGoogleNativeRoundTrip(t.Context(), batchexec.NewTestClient(ts.URL), "JFK", "LHR", "2026-10-25", "2026-11-01", SearchOptions{Adults: 1, Currency: "USD"}, "USD")
	if hits != 2 || len(got) != 1 {
		t.Fatalf("hits=%d got=%+v statuses=%+v", hits, got, statuses)
	}
	if got[0].Price != 700 || got[0].FareType != models.FareRoundTrip || len(got[0].Legs) != 2 || got[0].Legs[1].FlightNumber != "B6 777" {
		t.Fatalf("round trip = %+v", got[0])
	}
}

func TestGoogleNativeShellCannotBorrowIndependentReturn(t *testing.T) {
	shells := []models.FlightResult{{Provider: "google_flights", Price: 295, Warnings: []string{googleNativeRoundTripWarning}, Legs: []models.FlightLeg{{Direction: "outbound"}}}}
	inbound := []models.FlightResult{{Price: 100, Currency: "USD", Legs: []models.FlightLeg{{DepartureAirport: models.AirportInfo{Code: "LHR"}, ArrivalAirport: models.AirportInfo{Code: "JFK"}}}}}
	got, _ := enrichNativeReturnLegs(shells, inbound, 1)
	if len(got[0].Legs) != 1 || got[0].FareType == models.FareRoundTrip {
		t.Fatalf("fabricated roundtrip: %+v", got[0])
	}
}

func TestGooglePageMalformedItineraryIsParseFailure(t *testing.T) {
	for _, kind := range []string{"missing-leg", "bad-middle-leg", "zero-price", "wrong-route"} {
		t.Run(kind, func(t *testing.T) {
			body, err := os.ReadFile("testdata/google_flights_page.html")
			if err != nil {
				t.Fatal(err)
			}
			payload, err := batchexec.DecodeFlightPage(body)
			if err != nil {
				t.Fatal(err)
			}
			p := payload.([]any)
			entry := p[2].([]any)[0].([]any)[0].([]any)
			p[2] = []any{[]any{entry}}
			p[3] = []any{[]any{}}
			info := entry[0].([]any)
			switch kind {
			case "missing-leg":
				info[2] = []any{}
			case "bad-middle-leg":
				info[2] = append(info[2].([]any), nil)
			case "zero-price":
				entry[1].([]any)[0] = []any{nil, float64(0)}
			case "wrong-route":
				info[2].([]any)[0].([]any)[3] = "BOG"
			}
			raw, _ := json.Marshal(p)
			html := fmt.Sprintf(`<script>AF_initDataCallback({key: 'ds:1', data:%s, sideChannel:{}});</script>`, raw)
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(html)) }))
			defer ts.Close()
			_, err = searchGoogleFlightsWithClient(t.Context(), batchexec.NewTestClient(ts.URL), "JFK", "LHR", "2026-10-25", SearchOptions{Adults: 1, Currency: "USD"})
			if !errors.Is(err, models.ErrParseFailed) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestGooglePageRoundTripToken(t *testing.T) {
	token, err := flightPageToken("BOG", "MDE", "2026-11-12", SearchOptions{ReturnDate: "2026-11-19", Adults: 2, CabinClass: models.Business, MaxStops: models.OneStop, Alliances: []string{"SKYTEAM"}})
	if err != nil {
		t.Fatal(err)
	}
	const want = "CBwQAhopEgoyMDI2LTExLTEyKAEyB1NLWVRFQU1qBwgBEgNCT0dyBwgBEgNNREUaKRIKMjAyNi0xMS0xOSgBMgdTS1lURUFNagcIARIDTURFcgcIARIDQk9HQAFAAUgDcAGYAQE"
	if token != want {
		t.Fatalf("token = %s", token)
	}
}

func TestGooglePageUnsupportedFiltersDoNotDial(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("unsupported search dialed upstream") }))
	defer ts.Close()
	for _, opts := range []SearchOptions{{CheckedBags: 1}, {CarryOnBags: 1}, {ExcludeBasic: true}, {LessEmissions: true}, {Adults: 10}} {
		if _, err := searchGoogleFlightsWithClient(t.Context(), batchexec.NewTestClient(ts.URL), "BOG", "MDE", "2026-11-12", opts); err == nil {
			t.Fatalf("no refusal for %+v", opts)
		}
	}
}

func TestGooglePageFiltersKeepOnlyMatchingRows(t *testing.T) {
	flights := []models.FlightResult{
		{Price: 100, Duration: 100, Legs: []models.FlightLeg{{AirlineCode: "AA", DepartureTime: "2026-11-12T09:30"}}},
		{Price: 200, Duration: 100, Legs: []models.FlightLeg{{AirlineCode: "AA", DepartureTime: "2026-11-12T09:30"}}},
		{Price: 100, Duration: 200, Legs: []models.FlightLeg{{AirlineCode: "AA", DepartureTime: "2026-11-12T09:30"}}},
		{Price: 100, Duration: 100, Legs: []models.FlightLeg{{AirlineCode: "DL", DepartureTime: "2026-11-12T09:30"}}},
		{Price: 100, Duration: 100, Legs: []models.FlightLeg{{AirlineCode: "AA", DepartureTime: "2026-11-12T07:30"}}},
		{Price: 100, Duration: 100},
	}
	got := filterFlightPageResults(flights, SearchOptions{MaxPrice: 150, MaxDuration: 150, Airlines: []string{"AA"}, DepartAfter: "09:00", DepartBefore: "10:00"})
	if len(got) != 1 || got[0].Legs[0].DepartureTime != "2026-11-12T09:30" {
		t.Fatalf("got=%+v", got)
	}
	if len(flights) != 6 {
		t.Fatal("input mutated")
	}
}

// Opt-in single-call probe. It never deletes cache/cooldown state, retries a
// challenge, or selects another transport after refusal.
func TestGooglePageLiveProbe(t *testing.T) {
	if os.Getenv("TRVL_GOOGLE_PAGE_PROBE") != "1" {
		t.Skip("set TRVL_GOOGLE_PAGE_PROBE=1 for one live Google page request")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	opts := SearchOptions{Adults: 1, CabinClass: models.Economy, Currency: "USD"}
	if os.Getenv("TRVL_GOOGLE_PAGE_ROUNDTRIP") == "1" {
		opts.ReturnDate = "2026-11-19"
	}
	result, err := searchGoogleFlightsWithClient(ctx, batchexec.NewClient(), "BOG", "MDE", "2026-11-12", opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("ordinary page: success=%v, flights=%d", result.Success, result.Count)
	if !result.Success || result.Count == 0 {
		t.Fatal("no live offers")
	}
	for _, f := range result.Flights {
		finalAirport := "MDE"
		if opts.ReturnDate != "" {
			finalAirport = "BOG"
			if !flightHasInboundLeg(f) || f.FareType != models.FareRoundTrip {
				t.Fatalf("unpaired RT: %+v", f)
			}
		}
		if len(f.Legs) == 0 || f.Legs[0].DepartureAirport.Code != "BOG" || f.Legs[len(f.Legs)-1].ArrivalAirport.Code != finalAirport || f.Currency != "USD" || f.Price <= 0 {
			t.Fatalf("invalid quote: %+v", f)
		}
	}
}

func TestGooglePageEmptyAndMalformedAreDistinct(t *testing.T) {
	for _, tc := range []struct {
		name, body   string
		parseFailure bool
	}{
		{"empty", `<script>AF_initDataCallback({key: 'ds:1', data:[null,null,[[]],[[]]], sideChannel:{}});</script>`, false},
		{"missing", `<script>AF_initDataCallback({key: 'ds:0', data:[], sideChannel:{}});</script>`, true},
		{"malformed", `<script>AF_initDataCallback({key: 'ds:1', data:[broken], sideChannel:{}});</script>`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(tc.body)) }))
			defer ts.Close()
			got, err := searchGoogleFlightsWithClient(context.Background(), batchexec.NewTestClient(ts.URL), "BOG", "MDE", "2026-11-12", SearchOptions{Adults: 1})
			if tc.parseFailure {
				if !errors.Is(err, models.ErrParseFailed) {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if err != nil || !got.Success || got.Count != 0 {
				t.Fatalf("got = %+v, err = %v", got, err)
			}
		})
	}
}

func TestGooglePageChallengeStopsFurtherCalls(t *testing.T) {
	hits := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte(`<html><form action="https://www.google.com/sorry/index">unusual traffic</form></html>`))
	}))
	defer ts.Close()
	c := batchexec.NewTestClient(ts.URL)
	for range 2 {
		_, err := searchGoogleFlightsWithClient(context.Background(), c, "BOG", "MDE", "2026-11-12", SearchOptions{Adults: 1})
		if !errors.Is(err, models.ErrRateLimited) {
			t.Fatalf("err = %v", err)
		}
	}
	if hits != 1 {
		t.Fatalf("calls = %d, want 1", hits)
	}
}
