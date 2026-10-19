package realtime

import "testing"

// adr: 20261019102657 — preview features need the node flag and apid's
// endpoint mark; the registration request carries the mark.
func TestPreviewFeaturesNeedTheEndpointMark(t *testing.T) {
	on := &Manager{cfg: Config{ResumePreview: true, TicketPreview: true, AccessPreview: true, OccupancyPreview: true}}
	off := &Manager{}
	for _, tc := range []struct {
		m      *Manager
		marked bool
		want   bool
	}{
		{on, true, true}, {on, false, false}, {off, true, false}, {off, false, false},
	} {
		e := Endpoint{Preview: tc.marked}
		for name, got := range map[string]bool{
			"resume": tc.m.resumePreview(e), "tickets": tc.m.ticketPreview(e),
			"access": tc.m.accessPreview(e), "occupancy": tc.m.occupancyPreview(e),
		} {
			if got != tc.want {
				t.Errorf("%s with flags=%v marked=%v = %v, want %v", name, tc.m == on, tc.marked, got, tc.want)
			}
		}
	}
	if !(endpointRequest{Preview: true}).endpoint().Preview {
		t.Fatal("endpoint request dropped the preview mark")
	}
}
