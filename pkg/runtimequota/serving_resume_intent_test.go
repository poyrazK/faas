package runtimequota

import (
	"testing"

	"github.com/google/uuid"
)

func TestServingResumeIntentIdentity(t *testing.T) {
	g, _, _, _ := fixture(t)
	p := CapacityPermit{Grant: g, ID: uuid.NewString(), InstanceID: uuid.NewString(), RunID: uuid.NewString(), DeploymentID: uuid.NewString(), NodeID: uuid.NewString(), HostEpochID: uuid.NewString(), Kind: CapacityWarm}
	id := uuid.NewString()
	i, err := NewServingResumeIntent(p, id, false)
	if err != nil || i.Seat.Permit != p || i.Placement().Validate() != nil {
		t.Fatal(i, err)
	}
	again, err := NewServingResumeIntent(p, id, false)
	if err != nil || again != i {
		t.Fatal("retry changed its identity", again, err)
	}
	fresh, err := NewServingResumeIntent(p, uuid.NewString(), false)
	if err != nil || fresh.Seat.ID == i.Seat.ID || fresh.Seat.Permit != p {
		t.Fatal("new intent changed physical attempt or reused seat", fresh, err)
	}
	changed := p
	changed.DeploymentID = uuid.NewString()
	mutated, err := NewServingResumeIntent(changed, id, false)
	if err != nil || mutated.Seat.ID != i.Seat.ID {
		t.Fatal("changed claims obtained a new seat ID", mutated, err)
	}
	placement := i.Placement()
	placement.Seat.ID = uuid.NewString()
	if i.Seat.ID != again.Seat.ID {
		t.Fatal("placement alias changed intent")
	}
	for _, kind := range []string{CapacityPrime, CapacityAppTask} {
		changed.Kind = kind
		if _, err = NewServingResumeIntent(changed, id, false); err == nil {
			t.Fatal("ineligible purpose obtained serving intent", kind)
		}
	}
	i.Seat.ID = uuid.NewString()
	if i.Validate() == nil {
		t.Fatal("arbitrary seat ID accepted")
	}
}
