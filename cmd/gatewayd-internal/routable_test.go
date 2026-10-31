// adr: 732
package main

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestGatewayRoutableNeverSelectsAFork(t *testing.T) {
	running := state.Instance{ID: "i-1", NodeID: "n-1", State: string(state.StateRunning), Mode: string(state.InstanceModeNormal)}
	for _, tc := range []struct {
		name string
		edit func(*state.Instance)
		want bool
	}{
		{"running normal", func(*state.Instance) {}, true},
		{"running service", func(i *state.Instance) { i.Mode = string(state.InstanceModeService) }, true},
		{"running fork", func(i *state.Instance) { i.Mode = string(state.InstanceModeFork) }, false},
		{"parked", func(i *state.Instance) { i.State = string(state.StateParked) }, false},
		{"no node", func(i *state.Instance) { i.NodeID = "" }, false},
	} {
		instance := running
		tc.edit(&instance)
		if got := gatewayRoutable(instance); got != tc.want {
			t.Errorf("%s: routable = %v, want %v", tc.name, got, tc.want)
		}
	}
}
