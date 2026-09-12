package state

import (
	"context"
	"sort"
	"time"
)

// StatusUptimeBuckets returns one daily terminal-invocation bucket for the
// requested window. It is an optional Store capability because the public
// status endpoint must remain source-compatible with narrow test doubles and
// older integrations that only implement the core Store interface.
type StatusHistoryStore interface {
	StatusUptimeBuckets(ctx context.Context, since time.Time) ([]StatusUptimeBucket, error)
	ListStatusIncidentsSince(ctx context.Context, since time.Time, limit int) ([]StatusIncident, error)
}

// statusTerminal reports whether an invocation contributes to the public
// uptime denominator. Pending and dispatching rows are work in progress, not
// successes or failures, so excluding them avoids making a busy queue look
// like an outage.
func statusTerminal(inv Invocation) bool {
	if inv.Outcome != nil {
		return true
	}
	switch inv.State {
	case InvocationCompleted, InvocationFailed, InvocationCancelled, InvocationDeadLetter:
		return true
	default:
		return false
	}
}

func statusSuccessful(inv Invocation) bool {
	if inv.Outcome != nil {
		return *inv.Outcome == OutcomeSuccess
	}
	return inv.State == InvocationCompleted
}

// statusUptimeBuckets is the shared in-memory rollup used by MemStore. The
// SQL implementation in pgstore_status_history.go intentionally mirrors its
// terminal/success semantics.
func statusUptimeBuckets(invocations map[string]Invocation, since time.Time) []StatusUptimeBucket {
	type counts struct{ successful, total int64 }
	byDay := make(map[time.Time]counts)
	for _, inv := range invocations {
		if inv.CreatedAt.Before(since) || !statusTerminal(inv) {
			continue
		}
		day := time.Date(inv.CreatedAt.UTC().Year(), inv.CreatedAt.UTC().Month(), inv.CreatedAt.UTC().Day(), 0, 0, 0, 0, time.UTC)
		c := byDay[day]
		c.total++
		if statusSuccessful(inv) {
			c.successful++
		}
		byDay[day] = c
	}
	out := make([]StatusUptimeBucket, 0, len(byDay))
	for day, c := range byDay {
		out = append(out, StatusUptimeBucket{Day: day, Successful: c.successful, Total: c.total})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Day.Before(out[j].Day) })
	return out
}
