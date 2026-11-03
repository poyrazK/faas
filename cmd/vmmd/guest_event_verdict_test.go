package main

// adr: 20261031102000

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Refusals that apid answers 409 and 429 get the same answer in the guest,
// instead of a retryable 503 an app would repeat forever.
func TestGuestEventPublishVerdictMatchesPublishAPI(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want api.EventPublishVerdict
	}{
		"identity conflict": {fmt.Errorf("persist in-guest event publish: %w", state.ErrConflict),
			api.EventPublishVerdict{Status: http.StatusConflict, Code: api.CodeConflict}},
		"identity constraint": {fmt.Errorf("persist: %w", &pgconn.PgError{Code: "23505", ConstraintName: "event_fanout_identity_uniq"}),
			api.EventPublishVerdict{Status: http.StatusConflict, Code: api.CodeConflict}},
		"storage full": {fmt.Errorf("persist: %w", &state.EventStorageCapacityError{Resource: "events", Limit: 10, Observed: 11}),
			api.EventPublishVerdict{Status: http.StatusTooManyRequests, Code: "event_storage_capacity_exhausted", RetryAfterSeconds: api.EventStorageRetryAfterSeconds}},
	} {
		if got := guestEventPublishVerdict(tc.err); got != tc.want {
			t.Errorf("%s: verdict = %+v, want %+v", name, got, tc.want)
		}
	}
}
