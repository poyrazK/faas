// Package appwake holds explicit-wake decisions apid makes before it queues a
// wake for schedd.
package appwake

import "github.com/onebox-faas/faas/pkg/state"

// ResumableFrozenInstance returns the frozen VM of deploymentID that an
// explicit wake should resume: the one that served a request most recently,
// then the lowest id, the order schedd's thaw tries them in (ADR-1151). Fork
// instances are never a wake target.
//
// A thaw keeps the VM's own wake_id, which is its identity for vmmd and the
// config proof, so a wake that resumes it stamps no new instance. apid names
// the instance in the wake request so schedd thaws that one and the caller
// can wait on it (#6639).
func ResumableFrozenInstance(instances []state.Instance, deploymentID string) (state.Instance, bool) {
	var best state.Instance
	found := false
	for _, ins := range instances {
		if state.State(ins.State) != state.StateFrozen || ins.DeploymentID != deploymentID || ins.Mode == "fork" {
			continue
		}
		if !found || ins.LastRequestAt.After(best.LastRequestAt) ||
			ins.LastRequestAt.Equal(best.LastRequestAt) && ins.ID < best.ID {
			best, found = ins, true
		}
	}
	return best, found
}
