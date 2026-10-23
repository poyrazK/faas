package realtime

import (
	"encoding/json"
	"strings"
	"testing"
)

// adr: 20261021080640 — apid's per-account preview decisions narrow the
// daemon's preview switches, never widen them.

func TestEndpointPreviewsNarrowTheDaemonSwitches(t *testing.T) {
	on := Config{TicketPreview: true, ResumePreview: true, AccessPreview: true, OccupancyPreview: true}
	cases := []struct {
		name     string
		cfg      Config
		previews *EndpointPreviews
		want     bool
	}{
		{"no decision follows the switch", on, nil, true},
		{"decided off", on, &EndpointPreviews{}, false},
		{"decided on", on, &EndpointPreviews{Resume: true, Tickets: true, Access: true, Occupancy: true}, true},
		{"switch off wins over a decision", Config{}, &EndpointPreviews{Resume: true, Tickets: true, Access: true, Occupancy: true}, false},
	}
	for _, tc := range cases {
		e := Endpoint{Previews: tc.previews}
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
}
