package state

// rawIngressQuarantinedAppsLocked mirrors the durable public HTTP gate. The
// caller holds m.mu; management reads keep saved intent so a clean replacement
// can restore the listeners without recreating their public reservations.
func (m *MemStore) rawIngressQuarantinedAppsLocked() map[string]bool {
	live := make(map[string][]Deployment)
	for _, deployment := range m.deployments {
		if deployment.Status == DeployLive && IsProductionRoutingScope(deployment.Scope) {
			live[deployment.AppID] = append(live[deployment.AppID], deployment)
		}
	}
	apps := make(map[string]bool)
	for app, rows := range live {
		for _, deployment := range ProductionRoutingDeployments(rows) {
			if deployment.TrafficPercent > 0 && deployment.ParkedReason == string(ParkReasonSecurityScanRegressed) {
				apps[app] = true
				break
			}
		}
	}
	return apps
}
