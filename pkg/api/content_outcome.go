package api

import (
	"strconv"
	"time"
)

// Content outcomes on request telemetry (ADR-1188): how an anonymous GET
// response compared with the previous response for the same URL on the same
// gateway. Empty means the response was not observed.
const (
	// ContentOutcomeUncacheable: the edge cache would refuse to store the
	// response (status, Set-Cookie, Cache-Control: no-store/private, Vary, or
	// a body over the cache entry size).
	ContentOutcomeUncacheable = "uncacheable"
	// ContentOutcomeFirst: the first response seen for this URL.
	ContentOutcomeFirst = "first"

	contentOutcomeSame    = "same_"
	contentOutcomeChanged = "changed_"
	contentOutcomeLong    = "long"
)

// ContentOutcomeFor names the outcome of a response that was identical to
// (same) or different from the previous one for its URL, seen gap ago. The
// gap is bucketed by RouteContentWindows: a cache rule with that max-age
// would have served the previous body.
func ContentOutcomeFor(same bool, gap time.Duration) string {
	prefix := contentOutcomeChanged
	if same {
		prefix = contentOutcomeSame
	}
	for _, window := range RouteContentWindows {
		if gap < window {
			return prefix + strconv.Itoa(int(window/time.Second))
		}
	}
	return prefix + contentOutcomeLong
}

// ContentOutcomes is the closed set of non-empty outcomes, in a stable order.
func ContentOutcomes() []string {
	out := []string{ContentOutcomeUncacheable, ContentOutcomeFirst}
	for _, prefix := range []string{contentOutcomeSame, contentOutcomeChanged} {
		for _, window := range RouteContentWindows {
			out = append(out, prefix+strconv.Itoa(int(window/time.Second)))
		}
		out = append(out, prefix+contentOutcomeLong)
	}
	return out
}

// ValidContentOutcome reports whether s is empty or a known outcome.
func ValidContentOutcome(s string) bool {
	if s == "" {
		return true
	}
	for _, outcome := range ContentOutcomes() {
		if s == outcome {
			return true
		}
	}
	return false
}
