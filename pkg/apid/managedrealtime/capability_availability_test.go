package managedrealtime

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

type allowAccounts map[string]bool

func (a allowAccounts) Allows(id string) bool { return a[id] }

// adr: 20261019102657 — the capability registry reports a realtime capability
// available only when its routes would serve the account: a realtime node,
// the preview rollout, the daemon switch and the per-feature flag.
func TestCapabilityAvailableFollowsRouteGates(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil)
	in, out := state.Account{ID: "in"}, state.Account{ID: "out"}
	all := func() *Service {
		return &Service{
			Registrar:               &liveRouteRegistrar{},
			Preview:                 allowAccounts{"in": true},
			HistoryPreviewEnabled:   true,
			OccupancyPreviewEnabled: true,
			TicketsPreviewEnabled:   true,
			AccessPreviewEnabled:    true,
		}
	}
	preview := []string{"realtime-access", "realtime-channels", "realtime-occupancy", "realtime-publishers",
		"realtime-push", "realtime-tickets", "realtime-workers"}

	s := all()
	for _, key := range append([]string{"realtime", "realtime-connections"}, preview...) {
		if got, owned := s.CapabilityAvailable(r, in, key); !owned || !got {
			t.Errorf("%s in the rollout with every gate on = %v (owned %v), want available", key, got, owned)
		}
	}
	for _, key := range preview {
		if got, _ := s.CapabilityAvailable(r, out, key); got {
			t.Errorf("%s outside the rollout must be unavailable", key)
		}
	}
	for _, key := range []string{"realtime", "realtime-connections"} {
		if got, _ := s.CapabilityAvailable(r, out, key); !got {
			t.Errorf("%s is not a preview and must stay available outside the rollout", key)
		}
	}
	if _, owned := s.CapabilityAvailable(r, in, "object-storage"); owned {
		t.Fatal("non-realtime keys belong to the caller")
	}

	s = all()
	s.Registrar = nil
	for _, key := range append([]string{"realtime", "realtime-connections"}, preview...) {
		if got, _ := s.CapabilityAvailable(r, in, key); got {
			t.Errorf("%s without a realtime node must be unavailable", key)
		}
	}

	s = all()
	s.HistoryPreviewEnabled = false
	for _, key := range []string{"realtime-channels", "realtime-workers", "realtime-push", "realtime-publishers"} {
		if got, _ := s.CapabilityAvailable(r, in, key); got {
			t.Errorf("%s with retained history off must be unavailable", key)
		}
	}

	s = all()
	s.OccupancyPreviewEnabled = false
	if got, _ := s.CapabilityAvailable(r, in, "realtime-occupancy"); got {
		t.Error("occupancy with its daemon switch off and no platform flag must be unavailable")
	}
	s.PreviewReleased = func(_ *http.Request, _ state.Account, key string, legacy bool) bool {
		return key == platformflags.RealtimeOccupancy || legacy
	}
	if got, _ := s.CapabilityAvailable(r, in, "realtime-occupancy"); !got {
		t.Error("the occupancy platform flag must decide once written, as the route does")
	}
	s.NodesServePreview = func(string) bool { return false }
	if got, _ := s.CapabilityAvailable(r, in, "realtime-occupancy"); got {
		t.Error("occupancy the realtime nodes do not serve must be unavailable, as the route answers")
	}
}

func TestPreviewHiddenCapabilitiesOutsideRollout(t *testing.T) {
	s := &Service{Preview: allowAccounts{"in": true}}
	if got := s.PreviewHiddenCapabilities("in"); got != nil {
		t.Fatalf("in the rollout hidden = %v, want none", got)
	}
	got := s.PreviewHiddenCapabilities("out")
	if !slices.Contains(got, "realtime-occupancy") || !slices.Contains(got, "realtime-channels") || slices.Contains(got, "realtime") {
		t.Fatalf("outside the rollout hidden = %v", got)
	}
	got[0] = "mutated"
	if s.PreviewHiddenCapabilities("out")[0] == "mutated" {
		t.Fatal("callers must get a copy")
	}
	if len((&Service{}).PreviewHiddenCapabilities("in")) == 0 {
		t.Fatal("a Service without a rollout shows the preview to nobody")
	}
}
