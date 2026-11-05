package sched_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// Every recipient result maps to one closed routing outcome, so the routing
// alerts can tell routing that delivers from routing that only runs.
func TestEventRoutingOutcomeLabels(t *testing.T) {
	for _, tc := range []struct {
		progress state.PublishedEventRecipientProgress
		want     string
	}{
		{state.PublishedEventRecipientProgress{State: state.PublishedEventRecipientEnqueued}, wire.EventRoutingRouted},
		{state.PublishedEventRecipientProgress{State: state.PublishedEventRecipientFiltered}, wire.EventRoutingRouted},
		{state.PublishedEventRecipientProgress{State: state.PublishedEventRecipientPending, CapacityScope: "account"}, wire.EventRoutingWaiting},
		{state.PublishedEventRecipientProgress{State: state.PublishedEventRecipientPending, LastError: "wake failed"}, wire.EventRoutingRetry},
		{state.PublishedEventRecipientProgress{State: state.PublishedEventRecipientFailed}, wire.EventRoutingFailed},
	} {
		if got := sched.XEventRoutingOutcome(tc.progress); got != tc.want {
			t.Errorf("%s (capacity %q) = %s, want %s", tc.progress.State, tc.progress.CapacityScope, got, tc.want)
		}
	}
}
