package api

import "strings"

// Severity labels on the stateless.advisory audit row (ADR-047, Move 1
// PR-A). Mirrors pkg/dashboard/templates/audit_events.html's .badge.high /
// .badge.warn / .badge.info CSS.
const (
	StatelessAdvisorySeverityHigh = "high"
	StatelessAdvisorySeverityWarn = "warn"
	StatelessAdvisorySeverityInfo = "info"
)

// statelessAdvisoryHighPaths is the subset of guest-init's watched roots that
// warrants the "high" badge. pkg/dashboard's StatelessClosedPaths mirrors it
// (ADR-048 lockstep).
var statelessAdvisoryHighPaths = []string{
	"/data",
	"/db",
	"/var/lib/postgresql",
	"/var/lib/mysql",
}

// StatelessAdvisoryPathSeverity is "high" for a high root or any path under
// one, else "warn".
func StatelessAdvisoryPathSeverity(p string) string {
	for _, hp := range statelessAdvisoryHighPaths {
		if p == hp || strings.HasPrefix(p, hp+"/") {
			return StatelessAdvisorySeverityHigh
		}
	}
	return StatelessAdvisorySeverityWarn
}

// StatelessAdvisoryBatchSeverity is the highest severity in a batch, or
// "info" for an empty one. apid and vmmd stamp the same value, whichever of
// them writes the audit row.
func StatelessAdvisoryBatchSeverity(paths []string) string {
	if len(paths) == 0 {
		return StatelessAdvisorySeverityInfo
	}
	for _, p := range paths {
		if StatelessAdvisoryPathSeverity(p) == StatelessAdvisorySeverityHigh {
			return StatelessAdvisorySeverityHigh
		}
	}
	return StatelessAdvisorySeverityWarn
}
