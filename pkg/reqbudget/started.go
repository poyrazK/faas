// adr: 531
package reqbudget

import (
	"context"
	"time"
)

// WithStarted installs a budget measured from a trusted ingress timestamp.
// Elapsed lookup, upload and admission time is never refunded. An inherited
// budget or context deadline may tighten the result; neither can be extended.
// Callers must release cancel when the complete exchange finishes.
func WithStarted(parent context.Context, started time.Time, total, ceiling time.Duration, route, endpoint string) (context.Context, context.CancelFunc, Budget) {
	now := DefaultClock()
	if started.IsZero() || started.After(now) {
		started = now
	}
	if ceiling > 0 && total > ceiling {
		total = ceiling
	}
	deadline := started.Add(total)
	if inherited, ok := FromContext(parent); ok && inherited.Total > 0 {
		if earlier := inherited.Started.Add(inherited.Total); earlier.Before(deadline) {
			deadline = earlier
		}
	}
	if earlier, ok := parent.Deadline(); ok && earlier.Before(deadline) {
		deadline = earlier
	}
	ctx, cancel := context.WithDeadline(parent, deadline)
	b := Budget{Total: deadline.Sub(started), Started: started, Ceiling: ceiling,
		Route: route, Endpoint: endpoint, Source: SourceExplicit}
	ctx = context.WithValue(ctx, budgetParentKey{}, budgetBaseContext(parent))
	return NewContext(ctx, b), cancel, b
}
