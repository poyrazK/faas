// adr: 570
package gateway

// HealthyCountForDeployments counts routable residents in the caller's
// captured routing roster. It neither reads nor installs picker weights.
// Withdrawn residents remain in CapacityCount for scheduler cap accounting.
func (b *PGBackend) HealthyCountForDeployments(appID string, deployments []string) int {
	b.tgtMu.RLock()
	defer b.tgtMu.RUnlock()
	picker := b.appsPicker[appID]
	if picker == nil {
		return 0
	}
	seen := make(map[string]bool, len(deployments))
	count := 0
	for _, deployment := range deployments {
		if seen[deployment] {
			continue
		}
		seen[deployment] = true
		if set := picker.sets[deployment]; set != nil {
			for _, target := range set.entries {
				if target.routeReady() {
					count++
				}
			}
		}
	}
	return count
}

// deploymentCapacityCounter counts one deployment's cached instances,
// including those withdrawn by readiness.
type deploymentCapacityCounter interface {
	CapacityCountForDeployment(appID, deploymentID string) int
}

// coldBesideOtherInstances is ADR-199's overlap test on this gateway's cache:
// the deployment holds no instance while another deployment of the app holds
// one, as a release staged with no traffic does (ADR-20261031105227).
func (h *Handler) coldBesideOtherInstances(appID, deploymentID string) bool {
	counter, ok := h.backend.(deploymentCapacityCounter)
	return ok && deploymentID != "" && counter.CapacityCountForDeployment(appID, deploymentID) == 0 &&
		backendCapacityCount(h.backend, appID) > 0
}

// CapacityCountForDeployment counts one deployment's live cached instances,
// including targets temporarily withdrawn by readiness.
func (b *PGBackend) CapacityCountForDeployment(appID, deploymentID string) int {
	b.tgtMu.RLock()
	defer b.tgtMu.RUnlock()
	if picker := b.appsPicker[appID]; picker != nil {
		if set := picker.sets[deploymentID]; set != nil {
			return len(set.entries)
		}
	}
	return 0
}
