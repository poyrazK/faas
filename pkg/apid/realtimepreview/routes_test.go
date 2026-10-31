package realtimepreview

import "testing"

// adr: 20261019102657
func TestPathMatchesPreviewTemplatesOnly(t *testing.T) {
	for _, tc := range []struct {
		path string
		want bool
	}{
		{"/v1/apps/chat/realtime/endpoints/e1/tickets", true},
		{"/v1/apps/chat/realtime/endpoints/e1/tickets/t1", true},
		{"/v1/apps/chat/realtime/endpoints/e1//tickets", true},
		{"/v1/apps/chat/realtime/endpoints/e1/x/../occupancy", true},
		{"/v1/usage/realtime", true},
		{"/v1/realtime/workers/groups/g1/claims", true},
		{"/v1/apps/chat/realtime/endpoints/e1/channels/lobby/publish", false},
		{"/v1/apps/chat/realtime/endpoints/e1/connections", false},
		{"/v1/apps/chat/realtime/endpoints/e1", false},
		{"/v1/apps/chat/realtime/endpoints/e1/tickets/t1/extra", false},
	} {
		if got := Path(tc.path); got != tc.want {
			t.Errorf("Path(%q) = %t, want %t", tc.path, got, tc.want)
		}
	}
	if !Route("delete", "/v1/apps/{slug}/realtime/endpoints/{id}/tickets") || Route("GET", "/v1/apps/{slug}/realtime/endpoints/{id}/tickets") {
		t.Error("Route must match the method and template exactly, ignoring method case")
	}
}
