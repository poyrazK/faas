package main

import "github.com/onebox-faas/faas/pkg/state"

// gatewayRoutable reports whether the gateway may send a request to the
// instance: it is RUNNING with a known identity and node, and it is not a
// production fork or a platform snapshot refresh. ADR-732: a fork shares its
// app's live deployment but must never receive traffic, and neither may a
// refresh (ADR-20261106214916), so every target loader filters them here.
func gatewayRoutable(instance state.Instance) bool {
	return instance.State == string(state.StateRunning) &&
		instance.ID != "" && instance.NodeID != "" &&
		!state.NeverServes(instance.Mode)
}
