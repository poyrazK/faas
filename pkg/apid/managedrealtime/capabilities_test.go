package managedrealtime

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestCapabilityAvailableMatchesRouteGates(t *testing.T) {
	const account = "7b2c5e1e-4e8e-4a39-9d55-6a2c7f1d0a11"
	acct := state.Account{ID: account}
	rollout, err := ParsePreviewRollout(account)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil)
	keys := []string{"realtime-access", "realtime-tickets", "realtime-occupancy", "realtime-channels", "realtime-workers", "realtime-push", "realtime-publishers"}

	// Outside the rollout nothing is served, whatever the daemon switches say.
	s := &Service{AccessPreviewEnabled: true, TicketsPreviewEnabled: true, OccupancyPreviewEnabled: true, HistoryPreviewEnabled: true}
	for _, key := range keys {
		if got, ok := s.CapabilityAvailable(r, acct, key); !ok || got {
			t.Errorf("%s outside rollout: available=%v owned=%v", key, got, ok)
		}
	}

	// In the rollout with every switch off, only publishers (rollout-only) remain.
	s = &Service{Preview: rollout}
	for _, key := range keys {
		got, ok := s.CapabilityAvailable(r, acct, key)
		if !ok || got != (key == "realtime-publishers") {
			t.Errorf("%s with switches off: available=%v owned=%v", key, got, ok)
		}
	}

	// The retained-history switch alone opens channels, workers and push.
	s = &Service{Preview: rollout, HistoryPreviewEnabled: true}
	for key, want := range map[string]bool{"realtime-channels": true, "realtime-workers": true, "realtime-push": true, "realtime-occupancy": false, "realtime-tickets": false} {
		if got, _ := s.CapabilityAvailable(r, acct, key); got != want {
			t.Errorf("%s with history on: available=%v want %v", key, got, want)
		}
	}

	// A released flag the nodes cannot serve is off, like the route.
	s = &Service{Preview: rollout, OccupancyPreviewEnabled: true, NodesServePreview: func(string) bool { return false }}
	if got, _ := s.CapabilityAvailable(r, acct, "realtime-occupancy"); got {
		t.Error("occupancy advertised while realtime nodes lack it")
	}

	if _, ok := s.CapabilityAvailable(r, acct, "object-storage"); ok {
		t.Error("realtime claimed a capability it does not own")
	}
}
