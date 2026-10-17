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
