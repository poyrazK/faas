package state

import (
	"testing"
)

// A target deployment carries the build URLs captured from its source
// deployment (ADR-20261104120000 amendment 2026-11-21).
func TestMemCloneDeploymentCarriesPublicBuildURLs(t *testing.T) {
	urls := map[string]string{"VITE_API_URL": "https://api.example.com"}
	m := NewMemStore()
	if m.projectEnvironmentCloneWorkloads == nil {
		m.projectEnvironmentCloneWorkloads = map[string]map[string]projectCloneWorkloadRecord{}
	}
	if m.deploymentSidecarLayers == nil {
		m.deploymentSidecarLayers = map[string]DeploymentSidecarLayer{}
	}
	if m.sidecarSecretReloadSignals == nil {
		m.sidecarSecretReloadSignals = map[string]string{}
	}
	m.projectEnvironmentCloneWorkloads["op"] = map[string]projectCloneWorkloadRecord{}
	record := projectCloneWorkloadRecord{snapshot: projectCloneWorkloadSnapshot{PublicBuildURLs: urls}}
	m.attachCloneDeploymentLocked(projectEnvironmentCloneDeploymentInput{OperationID: "op", AppID: "app"}, record, Deployment{ID: "target"})
	if got := m.publicBuildURLs["target"]; len(got) != 1 || got["VITE_API_URL"] != urls["VITE_API_URL"] {
		t.Fatalf("target build URLs = %v", got)
	}
}
