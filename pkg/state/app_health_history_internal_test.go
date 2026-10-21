package state_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAppHealthHistoryReplicaTargetMeaning(t *testing.T) {
	a := api.AppHealthResponse{Status: "healthy", Phase: "serving", Scope: "default", Capacity: api.AppHealthCapacity{Known: true, Required: 1, Ready: 1}}
	initial, err := state.AppHealthKey(a)
	if err != nil {
		t.Fatal(err)
	}
	a.Capacity.Ready = 2
	moving, err := state.AppHealthKey(a)
	if err != nil || moving != initial {
		t.Fatal("moving replica counts created an event", err)
	}
	a.Capacity.Required = 2
	changed, err := state.AppHealthKey(a)
	if err != nil || changed == initial {
		t.Fatal("desired replica target change was omitted", err)
	}
}
