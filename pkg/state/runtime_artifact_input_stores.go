package state

// adr: 431. Conversion uses only the producer set captured inside evidence fences.

import "context"

var _ DeploymentRuntimeArtifactInputStore = (*MemStore)(nil)
var _ DeploymentRuntimeArtifactInputStore = (*PgStore)(nil)

func (m *MemStore) GetFreshDeploymentRuntimeArtifactInputs(ctx context.Context, accountID, appID, depID string) (DeploymentRuntimeArtifactInputs, error) {
	evidence, err := m.GetFreshDeploymentArtifactScanEvidence(ctx, accountID, appID, depID)
	if err != nil {
		return DeploymentRuntimeArtifactInputs{}, err
	}
	return deploymentRuntimeArtifactInputs(evidence)
}

func (s *PgStore) GetFreshDeploymentRuntimeArtifactInputs(ctx context.Context, accountID, appID, depID string) (DeploymentRuntimeArtifactInputs, error) {
	evidence, err := s.GetFreshDeploymentArtifactScanEvidence(ctx, accountID, appID, depID)
	if err != nil {
		return DeploymentRuntimeArtifactInputs{}, err
	}
	return deploymentRuntimeArtifactInputs(evidence)
}
