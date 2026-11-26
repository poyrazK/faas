package apidserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/managedrealtime"
	"github.com/onebox-faas/faas/pkg/realtime"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261019102657 — GET /v1/capabilities (read by the CLI and the MCP
// gregale_context tool) reports the realtime capabilities as their routes
// answer: a realtime node, the preview rollout and the daemon switch. It
// used to check only the capability flag (2026-11-22).
func TestGetCapabilitiesRealtimeFollowsRouteGates(t *testing.T) {
	const inID, outID = "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
	rollout, err := managedrealtime.ParsePreviewRollout(inID)
	if err != nil {
		t.Fatal(err)
	}
	read := func(s *server, accountID string) api.CapabilitiesResponse {
		t.Helper()
		recorder := httptest.NewRecorder()
		s.getCapabilities(recorder, httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil), state.Account{ID: accountID, Plan: api.PlanPro})
		var out api.CapabilitiesResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}

	s := &server{}
	if got := capabilityByKey(t, read(s, inID), "realtime"); got.Enabled {
		t.Fatalf("realtime without a realtime node = %+v, want unavailable", got)
	}

	s.realtime.Registrar = capabilityTestRegistrar{}
	s.realtime.Preview = rollout
	s.realtime.HistoryPreviewEnabled = true
	in := read(s, inID)
	for _, key := range []string{"realtime", "realtime-connections", "realtime-channels", "realtime-workers"} {
		if got := capabilityByKey(t, in, key); !got.Enabled {
			t.Errorf("%s in the rollout = %+v, want enabled", key, got)
		}
	}
	if got := capabilityByKey(t, in, "realtime-occupancy"); got.Enabled {
		t.Errorf("occupancy with its switch off = %+v, want unavailable", got)
	}
	if slices.Contains(in.HiddenCapabilities, "realtime-channels") {
		t.Errorf("hidden in the rollout = %v", in.HiddenCapabilities)
	}

	out := read(s, outID)
	if got := capabilityByKey(t, out, "realtime-channels"); got.Enabled {
		t.Errorf("realtime-channels outside the rollout = %+v, want unavailable", got)
	}
	if got := capabilityByKey(t, out, "realtime"); !got.Enabled {
		t.Errorf("released realtime outside the preview rollout = %+v, want enabled", got)
	}
	if !slices.Contains(out.HiddenCapabilities, "realtime-channels") || !slices.Contains(out.HiddenCapabilities, "realtime-occupancy") {
		t.Errorf("hidden outside the rollout = %v, want the realtime preview capabilities", out.HiddenCapabilities)
	}
}

// capabilityTestRegistrar stands in for a realtime node.
type capabilityTestRegistrar struct{}

func (capabilityTestRegistrar) RegisterEndpoint(context.Context, realtime.Endpoint) error {
	return nil
}
func (capabilityTestRegistrar) RemoveEndpoint(context.Context, string) error { return nil }

func TestMergeHiddenCapabilitiesSkipsDuplicates(t *testing.T) {
	got := mergeHiddenCapabilities([]string{"a", "b"}, []string{"b", "c"})
	if !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Fatalf("merged = %v", got)
	}
}
