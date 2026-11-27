package receivers

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// prod hunt 2026-11-21: a row the gateway served just before its app was
// purged failed the rollup window's app foreign key on every retry; the
// "analytics_backpressure" answer made the gateway drop its whole batch and
// flag its telemetry unhealthy. A purged app's row is now not retained.
func TestRequestTelemetryForPurgedAppIsNotRetained(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		expect string
	}{
		{"purged app", fmt.Errorf("%w: app gone", state.ErrNotFound), rtOutcomeNotRetained},
		{"transient", errors.New("connection reset"), rtOutcomeAnalyticsBackpressure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := &consumerTelemetryStore{account: state.Account{Plan: api.PlanPro}}
			store := &durableRollupTestStore{windows: map[uuid.UUID]string{}, markerErr: tc.err}
			a := NewRequestAnalyticsRollups(store, nil, nil)
			a.now = func() time.Time { return time.Now().Add(3 * time.Minute) }
			receiver := NewRequestTelemetryReceiver(raw, nil, nil, true)
			a.applyRequestTelemetry(receiver)
			out := receiver.HandleOne(t.Context(), rollupTelemetryRequest(uuid.NewString(), uuid.NewString(), uuid.NewString(), time.Now()))
			if out.GetOutcome() != tc.expect {
				t.Fatalf("outcome = %q, want %q", out.GetOutcome(), tc.expect)
			}
		})
	}
}
