package realtime

import (
	"encoding/json"
	"strings"
	"testing"
)

// adr: 20261021080640 — apid's per-account preview decisions narrow the
// daemon's preview switches, never widen them.
// adr: 20261019102657 — preview features also need apid's mark that the
// endpoint's account is in the realtime preview rollout.

func TestEndpointPreviewsNarrowTheDaemonSwitches(t *testing.T) {
	on := Config{TicketPreview: true, ResumePreview: true, AccessPreview: true, OccupancyPreview: true}
	all := &EndpointPreviews{Resume: true, Tickets: true, Access: true, Occupancy: true}
	cases := []struct {
		name     string
		cfg      Config
		marked   bool
		previews *EndpointPreviews
		want     bool
	}{
		{"no decision follows the switch", on, true, nil, true},
		{"decided off", on, true, &EndpointPreviews{}, false},
		{"decided on", on, true, all, true},
		{"switch off wins over a decision", Config{}, true, all, false},
		{"unmarked endpoint stays off", on, false, all, false},
		{"unmarked endpoint without a decision stays off", on, false, nil, false},
	}
	for _, tc := range cases {
		e := Endpoint{Preview: tc.marked, Previews: tc.previews}
		got := []bool{e.resumeOn(tc.cfg), e.ticketsOn(tc.cfg), e.accessOn(tc.cfg), e.occupancyOn(tc.cfg)}
		for i, value := range got {
			if value != tc.want {
				t.Errorf("%s: preview %d = %t, want %t", tc.name, i, value, tc.want)
			}
		}
	}
}

func TestEndpointRequestCarriesPreviews(t *testing.T) {
	without, _ := json.Marshal(endpointRequest{ID: "e"})
	if strings.Contains(string(without), "previews") {
		t.Fatalf("an endpoint without decisions must not send previews (older realtimed rejects it): %s", without)
	}
	data, err := json.Marshal(endpointRequest{ID: "e", Previews: &EndpointPreviews{Tickets: true}})
	if err != nil {
		t.Fatal(err)
	}
	var decoded endpointRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if p := decoded.endpoint().Previews; p == nil || !p.Tickets || p.Access {
		t.Fatalf("round trip = %+v", p)
	}
	if !(endpointRequest{Preview: true}).endpoint().Preview {
		t.Fatal("endpoint request dropped the preview mark")
	}
}
