package state

import "github.com/onebox-faas/faas/pkg/api"

// ComputeRegionAllowsNode is deliberately independent of capacity and affinity.
// Unlabelled nodes cannot host an explicitly region-bound app.
func ComputeRegionAllowsNode(region string, node ComputeNode) bool {
	return region == "" || node.Region != nil && *node.Region == region
}

func (m *MemStore) checkComputeRegionLocked(app App, nodeID string) error {
	if !api.ValidComputeRegion(app.ComputeRegion) {
		return ErrInvalidArgument
	}
	if app.ComputeRegion == "" || nodeID == "" {
		return nil
	}
	if !ComputeRegionAllowsNode(app.ComputeRegion, m.computeNodes[nodeID]) {
		return ErrConflict
	}
	return nil
}

func (m *MemStore) checkNodeRegionChangeLocked(old, next ComputeNode) error {
	if derefString(old.Region) == derefString(next.Region) {
		return nil
	}
	for _, app := range m.apps {
		if app.ComputeRegion != "" && app.ComputeRegion == derefString(old.Region) {
			return ErrConflict
		}
	}
	for _, job := range m.jobs {
		if job.ComputeRegion != "" && job.ComputeRegion == derefString(old.Region) {
			return ErrConflict
		}
	}
	for _, instance := range m.instances {
		if instance.NodeID == old.ID && m.apps[instance.AppID].ComputeRegion != "" &&
			(instance.State == "waking" || instance.State == "cold_booting" || instance.State == "running" ||
				instance.State == "snapshotting" || instance.State == "migrating" || instance.State == "warm_paused") {
			return ErrConflict
		}
	}
	return nil
}

func (m *MemStore) checkJobComputeRegionLocked(job Job, nodeID string) error {
	if job.ComputeRegion != "" && nodeID == "" {
		return ErrConflict
	}
	return m.checkComputeRegionLocked(App{ComputeRegion: job.ComputeRegion}, nodeID)
}

func (m *MemStore) checkInstanceComputeRegionLocked(instance Instance, nodeID string) error {
	if instance.JobID != "" {
		return m.checkJobComputeRegionLocked(m.jobs[instance.JobID], nodeID)
	}
	return m.checkComputeRegionLocked(m.apps[instance.AppID], nodeID)
}
