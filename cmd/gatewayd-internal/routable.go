package main

import "github.com/onebox-faas/faas/pkg/state"

// gatewayRoutable reports whether the gateway may send a request to the
// instance: it is RUNNING with a known identity and node, and it is not a
// production fork. ADR-732: a fork shares its app's live deployment but must
// never receive traffic, so every target loader filters it here.
func gatewayRoutable(instance state.Instance) bool {
	return instance.State == string(state.StateRunning) &&
		instance.ID != "" && instance.NodeID != "" &&
		!state.IsFork(instance.Mode)
}
