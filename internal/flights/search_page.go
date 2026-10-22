package flights

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// Search-page tfs fields are documented by the MIT-licensed fli project:
// https://github.com/punitarani/fli/blob/881aee5/fli/search/_proto.py
// Unlike the RPC, the ordinary page requires no browser-generated bgr token.
func flightPageToken(origin, destination, date string, opts SearchOptions) (string, error) {
	if opts.CarryOnBags > 0 || opts.CheckedBags > 0 || opts.ExcludeBasic || opts.LessEmissions {
		return "", fmt.Errorf("google search-page transport does not support bag pricing, basic-economy exclusion or emissions filtering")
	}
	adults := opts.Adults
	if adults <= 0 {
		adults = 1
	}
	if adults > 9 {
		return "", fmt.Errorf("google search-page transport supports at most 9 passengers")
	}
	cabin := int(opts.CabinClass)
	if cabin == 0 {
		cabin = 1
	}
	payload := protoInt(nil, 1, 28)
	payload = protoInt(payload, 2, 2)
	payload = append(payload, flightPageSegment(origin, destination, date, opts)...)
	trip := uint64(2)
	if opts.ReturnDate != "" {
		payload = append(payload, flightPageSegment(destination, origin, opts.ReturnDate, opts)...)
		trip = 1
	}
	for range adults {
		payload = protoInt(payload, 8, 1)
	}
	payload = protoInt(payload, 9, uint64(cabin))
	payload = protoInt(payload, 14, 1)
	payload = protoInt(payload, 19, trip)
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func flightPageSegment(origin, destination, date string, opts SearchOptions) []byte {
	body := protoBytes(nil, 2, []byte(date))
	if opts.MaxStops > 0 {
		body = protoInt(body, 5, uint64(opts.MaxStops-1))
	}
	for _, alliance := range opts.Alliances {
		body = protoBytes(body, 6, []byte(strings.ToUpper(alliance)))
	}
	if len(opts.pageOutbound) > 0 && opts.pageOutbound[0].DepartureAirport.Code == origin {
		for _, leg := range opts.pageOutbound {
			selected := protoBytes(nil, 1, []byte(leg.DepartureAirport.Code))
			selected = protoBytes(selected, 2, []byte(leg.DepartureTime[:10]))
			selected = protoBytes(selected, 3, []byte(leg.ArrivalAirport.Code))
			selected = protoBytes(selected, 5, []byte(leg.AirlineCode))
			number := strings.TrimSpace(strings.TrimPrefix(leg.FlightNumber, leg.AirlineCode))
			selected = protoBytes(selected, 6, []byte(number))
			body = protoBytes(body, 4, selected)
		}
	}
	body = protoBytes(body, 13, protoBytes(protoInt(nil, 1, 1), 2, []byte(origin)))
	body = protoBytes(body, 14, protoBytes(protoInt(nil, 1, 1), 2, []byte(destination)))
	return protoBytes(nil, 3, body)
}

// A return board is quoted against an explicitly selected outbound. Its row's
// price is the complete round-trip total, not the first board's indicative fare
// or the sum of independently searched one-ways. Bound this to one outbound.
func pairedGooglePageRoundTrip(ctx context.Context, client *batchexec.Client, origin, destination, date string, opts SearchOptions, outbound []models.FlightResult) (*models.FlightSearchResult, error) {
	candidates := topPricedFlights(outbound, 1)
	if len(candidates) == 0 {
		return &models.FlightSearchResult{Success: true, TripType: "round_trip"}, nil
	}
	selected := candidates[0]
	pinned := opts
	pinned.pageOutbound = selected.Legs
	result, err := searchGoogleFlightsWithClient(ctx, client, origin, destination, date, pinned)
	if err != nil {
		return result, err
	}
	for i := range result.Flights {
		ret := &result.Flights[i]
		legs := taggedLegs(selected.Legs, "outbound")
		legs = append(legs, taggedLegs(ret.Legs, "inbound")...)
		ret.Legs = legs
		ret.Duration += selected.Duration
		ret.Stops += selected.Stops
		ret.FareType = models.FareRoundTrip
		ret.BookingURL = buildFlightBookingURL(origin, destination, date, opts.ReturnDate, opts.Currency)
	}
	return result, nil
}

func protoInt(dst []byte, field, value uint64) []byte {
	dst = binary.AppendUvarint(dst, field<<3)
	return binary.AppendUvarint(dst, value)
}

func protoBytes(dst []byte, field uint64, value []byte) []byte {
	dst = binary.AppendUvarint(dst, field<<3|2)
	dst = binary.AppendUvarint(dst, uint64(len(value)))
	return append(dst, value...)
}

func emptyFlightPage(payload any) bool {
	arr, ok := payload.([]any)
	if !ok || len(arr) < 4 {
		return false
	}
	for _, index := range []int{2, 3} {
		if arr[index] == nil {
			continue
		}
		bucket, ok := arr[index].([]any)
		if !ok || len(bucket) == 0 {
			return false
		}
		rows, ok := bucket[0].([]any)
		if !ok || len(rows) != 0 {
			return false
		}
	}
	return true
}

func decodeFlightPage(body []byte) (any, error) {
	payload, err := batchexec.DecodeFlightPage(body)
	if err != nil {
		return nil, fmt.Errorf("%v: %w", err, models.ErrParseFailed)
	}
	return payload, nil
}

func validateFlightPageRows(raw []any, flights []models.FlightResult, origin, destination, date string) error {
	bad := func() error {
		return fmt.Errorf("google search page contains an incomplete or mismatched itinerary: %w", models.ErrParseFailed)
	}
	if len(raw) != len(flights) {
		return bad()
	}
	for i, f := range flights {
		entry, ok := raw[i].([]any)
		if !ok || len(entry) < 2 {
			return bad()
		}
		info, ok := entry[0].([]any)
		if !ok || len(info) < 3 {
			return bad()
		}
		legs, ok := info[2].([]any)
		if !ok || len(legs) != len(f.Legs) || len(legs) == 0 {
			return bad()
		}
		if f.Price <= 0 || f.Duration <= 0 || !isKnownCurrency(f.Currency) {
			return bad()
		}
		if f.Legs[0].DepartureAirport.Code != origin || f.Legs[len(f.Legs)-1].ArrivalAirport.Code != destination || !strings.HasPrefix(f.Legs[0].DepartureTime, date+"T") {
			return bad()
		}
		for _, leg := range f.Legs {
			if len(leg.DepartureAirport.Code) != 3 || len(leg.ArrivalAirport.Code) != 3 || leg.AirlineCode == "" || leg.FlightNumber == "" || leg.Duration <= 0 {
				return bad()
			}
			if _, err := time.Parse("2006-01-02T15:04", leg.DepartureTime); err != nil {
				return bad()
			}
			if _, err := time.Parse("2006-01-02T15:04", leg.ArrivalTime); err != nil {
				return bad()
			}
		}
	}
	return nil
}

// Page results are a bounded board. Local filters never claim Google backfilled
// it after applying price, duration, carrier, or departure-time constraints.
func filterFlightPageResults(flights []models.FlightResult, opts SearchOptions) []models.FlightResult {
	out := make([]models.FlightResult, 0, len(flights))
	for _, f := range flights {
		if opts.MaxPrice > 0 && f.Price > float64(opts.MaxPrice) {
			continue
		}
		if opts.MaxDuration > 0 && f.Duration > opts.MaxDuration {
			continue
		}
		if len(opts.Airlines) > 0 {
			matched := false
			for _, leg := range f.Legs {
				for _, airline := range opts.Airlines {
					if strings.EqualFold(leg.AirlineCode, airline) {
						matched = true
					}
				}
			}
			if !matched {
				continue
			}
		}
		if opts.DepartAfter != "" || opts.DepartBefore != "" {
			if len(f.Legs) == 0 || len(f.Legs[0].DepartureTime) < 16 {
				continue
			}
			clock := f.Legs[0].DepartureTime[11:16]
			if opts.DepartAfter != "" && clock < opts.DepartAfter || opts.DepartBefore != "" && clock > opts.DepartBefore {
				continue
			}
		}
		out = append(out, f)
	}
	return out
}
