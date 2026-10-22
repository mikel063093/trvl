# Google Flights page transport

The Navegante fork searches the ordinary Google Flights page with a protobuf `tfs` query and the requested `curr`/locale. It reads the inline `AF_initDataCallback` `ds:1` payload. The retired shopping RPC is not retried as a fallback. HTTP uses Go's standard transport and a compatible User-Agent that also identifies trvl; this path does not use TLS impersonation, browser challenge tokens or CAPTCHA handling.

One-way searches make one page lookup. Native round trips are bounded to the cheapest outbound: a second page query pins its exact operating segments, and the returned board supplies both the matching return and the current total price. An independently searched inbound cannot establish which return belongs to the first board's price. Google outbound-only shells therefore remain incomplete instead of being enriched with an arbitrary one-way. Existing Kiwi indicative display enrichment retains its explicit warning; consumers requiring a confirmed itinerary must reject it.

Flight rows must include complete segment identities, route/date, currency, positive prices/durations and valid timestamps. Malformed pages are parse failures, while an explicitly empty board is an empty success. The page transport refuses bag repricing, basic-economy exclusion and emissions filters it cannot honor. Supported local filters apply only to the returned board and do not imply that Google backfilled the results.

Explicit challenge/consent pages and HTTP403/429 arm the existing shared, persisted Google refusal deadline. Further calls stop until it expires. A valid provider `Retry-After` is preserved rather than shortened to fifteen minutes. Response caches remain five-minute caches of valid flight pages; no quota/cooldown or user cache is deleted.

Protocol references: [fli query fields](https://github.com/punitarani/fli/blob/881aee5/fli/search/_proto.py), [fli transport](https://github.com/punitarani/fli/blob/881aee5/fli/search/flights.py). The trimmed flight fixture is accompanied by its MIT license. A second trimmed BOG–MDE page was captured during a live ordinary-HTTP probe on 2026-10-22.

Verification: `go test ./...`, `go build ./...`, `go vet ./...`. Live probes are opt-in: `TRVL_GOOGLE_PAGE_PROBE=1 go test ./internal/flights -run '^TestGooglePageLiveProbe$'`; add `TRVL_GOOGLE_PAGE_ROUNDTRIP=1` for the bounded paired-return lookup. They respect existing refusal state and never clear it before running.

Tracks [Navegante issue238](https://github.com/go-cristian/cruise-commander/issues/238).
