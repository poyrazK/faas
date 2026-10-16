package api

import (
	"strconv"
	"strings"
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

	ContentOutcomeDeploymentPrefix = "deployment_v1:"

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

// ContentOutcomes is the legacy base vocabulary, in a stable order.
// Deployment-isolated observations use ContentOutcomeDeploymentPrefix plus a base outcome.
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

// ValidContentOutcome accepts empty, legacy or deployment-isolated known outcomes.
func ValidContentOutcome(s string) bool {
	if s == "" {
		return true
	}
	s = strings.TrimPrefix(s, ContentOutcomeDeploymentPrefix)
	if s == "" {
		return false
	}
	for _, outcome := range ContentOutcomes() {
		if s == outcome {
			return true
		}
	}
	return false
}
