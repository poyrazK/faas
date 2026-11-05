package main

// adr: 20261031102000

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type guestEventLookup struct{ err error }

func (s guestEventLookup) LookupPublishedEventAcceptance(context.Context, string, []byte) (state.PublishedEventAcceptance, error) {
	return state.PublishedEventAcceptance{Duplicate: s.err == nil}, s.err
}

func TestGuestEventEnvelopeLimitPreservesDuplicates(t *testing.T) {
	oversized := []byte(`"` + strings.Repeat("x", int(api.WorkflowRunInputMaxBytes)) + `"`)
	for _, tc := range []struct {
		name  string
		store any
		want  error
	}{
		{"new event", guestEventLookup{state.ErrNotFound}, errGuestEventEnvelopeTooLarge},
		{"retained duplicate", guestEventLookup{}, nil},
		{"conflicting duplicate", guestEventLookup{state.ErrConflict}, state.ErrConflict},
		{"no lookup", nil, errGuestEventEnvelopeTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkGuestEventEnvelope(t.Context(), tc.store, "account", oversized); !errors.Is(err, tc.want) {
				t.Fatalf("check error=%v, want %v", err, tc.want)
			}
		})
	}
	if err := checkGuestEventEnvelope(t.Context(), nil, "account", []byte(`{"value":1}`)); err != nil {
		t.Fatalf("small envelope: %v", err)
	}
}

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
		"event envelope too large": {errGuestEventEnvelopeTooLarge,
			api.EventPublishVerdict{Status: http.StatusRequestEntityTooLarge, Code: "event_envelope_too_large"}},
	} {
		if got := guestEventPublishVerdict(tc.err); got != tc.want {
			t.Errorf("%s: verdict = %+v, want %+v", name, got, tc.want)
		}
	}
}
