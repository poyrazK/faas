package githubd

import (
	"github.com/onebox-faas/faas/pkg/hostidentity"
	"github.com/onebox-faas/faas/pkg/state"
)

// EnvironmentBranchWorkloadURL is the stable URL of an app's workload in
// environment branch scope of its project (ADR-20261113121342): the host
// apid reports for the branch, built from the branch's slot, so a reset
// keeps it. Empty when the IDs do not make a host.
func EnvironmentBranchWorkloadURL(projectID, scope, appID string) string {
	if projectID == "" || scope == "" || appID == "" {
		return ""
	}
	host := hostidentity.BuildEnvironmentHost(hostidentity.DeploySuffix(), state.EnvironmentBranchSlotID(projectID, scope), appID)
	if host == "" {
		return ""
	}
	return "https://" + host
}
