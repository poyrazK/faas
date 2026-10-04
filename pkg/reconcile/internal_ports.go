package reconcile

import (
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
)

// portsWithInternal applies a workload's compose expose: listeners to an
// app's port declarations (ADR-568). Only the internal subset is
// source-owned: public listeners declared through the API are kept, except
// that a source-declared internal TCP port takes over a public entry for the
// same port, so expose: can never leave a listener published. nil internal
// means the source said nothing, so the declarations are returned unchanged.
func portsWithInternal(existing, internal []api.WorkloadPort) []api.WorkloadPort {
	if internal == nil {
		return existing
	}
	internalTCP := make(map[int]struct{}, len(internal))
	for _, port := range internal {
		internalTCP[port.Port] = struct{}{}
	}
	out := make([]api.WorkloadPort, 0, len(existing)+len(internal))
	for _, port := range existing {
		if port.Internal {
			continue
		}
		if _, taken := internalTCP[port.Port]; taken && port.EffectiveProtocol() == api.WorkloadPortTCP {
			continue
		}
		out = append(out, port)
	}
	out = append(out, internal...)
	if len(out) > api.WorkloadPortCapMax {
		// Keep every source-declared internal listener; drop the oldest
		// public extras beyond the cap rather than fail the whole apply.
		out = out[len(out)-api.WorkloadPortCapMax:]
	}
	if len(out) == 0 && existing == nil {
		return []api.WorkloadPort{}
	}
	return out
}

// internalPortsChanged reports whether applying internal would change the
// app's internal listener set.
func internalPortsChanged(existing, internal []api.WorkloadPort) bool {
	if internal == nil {
		return false
	}
	var current []int
	for _, port := range existing {
		if port.Internal {
			current = append(current, port.Port)
		}
	}
	desired := make([]int, 0, len(internal))
	for _, port := range internal {
		desired = append(desired, port.Port)
	}
	slices.Sort(current)
	slices.Sort(desired)
	return !slices.Equal(current, desired)
}
