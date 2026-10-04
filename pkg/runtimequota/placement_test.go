package runtimequota

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRuntimePlacementCanonicalTransport(t *testing.T) {
	g, _, _, _ := fixture(t)
	p := Placement{Permit: capacityPermitFixture(g)}
	s := ServingSeat{Permit: p.Permit, ID: p.Permit.ID}
	p.Seat = &s
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePlacement(raw)
	if err != nil || decoded.Permit != p.Permit || *decoded.Seat != *p.Seat {
		t.Fatalf("roundtrip: %+v %v", decoded, err)
	}
	for _, bad := range [][]byte{append(raw, ' '), append([]byte(`{"Permit":null,`), raw[1:]...), append([]byte(`{"unknown":true,`), raw[1:]...), []byte(strings.Repeat("x", 20000))} {
		if _, err := DecodePlacement(bad); err == nil {
			t.Fatal("ambiguous placement admitted")
		}
	}
	clone := p.Clone()
	clone.Seat.Rollout = true
	if p.Seat.Rollout {
		t.Fatal("clone shares mutable seat")
	}
}
