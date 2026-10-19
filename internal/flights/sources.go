package flights

import (
	"sort"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// Provider ids a caller may name in SearchOptions.Sources. They are the ids the
// provider statuses already report.
const (
	SourceGoogleFlights = "google_flights"
	SourceKiwi          = "kiwi"
	SourceSkiplagged    = "skiplagged"
	SourceRyanair       = "ryanair"
	SourceWizzair       = "wizzair"
	SourceTransavia     = "transavia"
	SourceEasyjet       = "easyjet"
	SourceVueling       = "vueling"
	SourceNorwegian     = "norwegian"
	SourceAFKLM         = "afklm"
)

// sourceAliases maps the spellings callers use to the ids above.
var sourceAliases = map[string]string{
	"google":        SourceGoogleFlights,
	"googleflights": SourceGoogleFlights,
	"wizz":          SourceWizzair,
	"wizz_air":      SourceWizzair,
	"af-klm":        SourceAFKLM,
	"airfranceklm":  SourceAFKLM,
}

// NormalizeSources lower-cases, trims, resolves aliases, de-duplicates and sorts
// a caller-supplied provider list so equal lists compare (and cache) equal.
// An empty result means "every provider", the default.
func NormalizeSources(raw []string) []string {
	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		for _, part := range strings.Split(item, ",") {
			id := strings.ToLower(strings.TrimSpace(part))
			if id == "" {
				continue
			}
			if alias, ok := sourceAliases[id]; ok {
				id = alias
			}
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// sourceAllowed reports whether the search may query the provider. With no
// Sources every provider is allowed, which is the unchanged default.
//
// Google Flights is not gated here: it is queried first on every search because
// its answer fixes the session currency the other providers are priced in.
func (o SearchOptions) sourceAllowed(id string) bool {
	if len(o.Sources) == 0 {
		return true
	}
	for _, s := range o.Sources {
		if s == id {
			return true
		}
	}
	return false
}

// sourceSkipped is the outcome of a provider the caller left out of
// SearchOptions.Sources: reported as skipped, so the provider statuses explain
// the absence, and never queried.
func sourceSkipped(id, name string) providerOutcome {
	return providerOutcome{status: models.ProviderStatus{
		ID:      id,
		Name:    name,
		Status:  "skipped",
		Error:   "not in the requested sources",
		FixHint: "leave the sources option empty to query every provider",
	}}
}
