package state

import (
	"context"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
)

var _ EnvironmentGitOpsActivationEvidenceStore = (*MemStore)(nil)
var _ EnvironmentGitOpsCurrentWorkloadEvidenceStore = (*MemStore)(nil)

func (m *MemStore) EnvironmentGitOpsActivationEvidence(ctx context.Context, lease EnvironmentGitOpsLease, reviewed environmentsync.Plan) (EnvironmentWorkloadActivationEvidence, error) {
	if err := ctx.Err(); err != nil {
		return EnvironmentWorkloadActivationEvidence{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	memory, err := m.gitOpsLeaseLocked(lease, time.Now())
	if err != nil {
		return EnvironmentWorkloadActivationEvidence{}, err
	}
	graph, exists := memory.graphs[preparationGraphKey(lease.Source.Generation, reviewed.Hash)]
	if !exists {
		for _, current := range memory.graphs {
			if current.Generation == lease.Source.Generation && current.RevisionID == lease.Revision.ID {
				return EnvironmentWorkloadActivationEvidence{}, ErrConflict
			}
		}
		return EnvironmentWorkloadActivationEvidence{}, ErrNotFound
	}
	if graph.SourceID != memory.source.ID || graph.EnvironmentID != memory.source.EnvironmentID ||
		graph.Generation != memory.source.Generation || graph.IntentVersion != memory.source.IntentVersion ||
		graph.RevisionID != memory.source.ApprovedRevisionID || graph.PlanHash != reviewed.Hash {
		return EnvironmentWorkloadActivationEvidence{}, ErrConflict
	}
	evidence := m.environmentWorkloadActivationEvidenceLocked(memory, graph)
	// Activation intentionally publishes candidates as live deployments. That
	// changes the observed source baseline after the reviewed plan was applied,
	// so only an unactivated graph can be checked against the pre-activation
	// candidate plan. Once active, the exact graph and its runtime/serving
	// receipts are the evidence for this approved revision.
	if !evidence.Activated {
		if _, _, err := m.environmentCandidateInputsLocked(lease, reviewed); err != nil {
			return EnvironmentWorkloadActivationEvidence{}, err
		}
	}
	return evidence, nil
}

func (m *MemStore) CurrentEnvironmentGitOpsWorkloadEvidence(ctx context.Context, accountID, sourceID string) (*EnvironmentWorkloadActivationEvidence, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	memory, exists := m.environmentGitOps[sourceID]
	if !exists || memory.source.AccountID != accountID {
		return nil, ErrNotFound
	}
	if memory.source.Detached || memory.source.Suspended || memory.source.ApprovedRevisionID == "" {
		return nil, nil
	}
	revision, exists := memory.revisions[memory.source.ApprovedRevisionID]
	if !exists {
		return nil, ErrConflict
	}
	desired, err := desiredEnvironmentRevision(revision)
	if err != nil {
		return nil, err
	}
	snapshot := m.gitOpsSnapshotLocked(memory)
	observed, err := compileGitOpsObservation(snapshot, desired)
	if err != nil {
		return nil, err
	}
	plan, err := environmentGitOpsPlan(memory.source, revision, desired, observed, false)
	if err != nil {
		return nil, err
	}
	graph, exists := memory.graphs[preparationGraphKey(memory.source.Generation, plan.Hash)]
	if !exists || graph.SourceID != memory.source.ID || graph.EnvironmentID != memory.source.EnvironmentID ||
		graph.RevisionID != revision.ID || graph.Generation != memory.source.Generation || graph.IntentVersion != memory.source.IntentVersion ||
		graph.DefinitionDigest != revision.Digest || graph.PlanHash != plan.Hash {
		return nil, nil
	}
	evidence := m.environmentWorkloadActivationEvidenceLocked(memory, graph)
	return &evidence, nil
}

func (m *MemStore) environmentWorkloadActivationEvidenceLocked(memory *environmentGitOpsMemory, graph EnvironmentWorkloadGraph) EnvironmentWorkloadActivationEvidence {
	activeTargets := m.environmentGraphActiveReleaseTargetsLocked(memory.source, graph)
	activatedCandidates, hasCandidates := true, false
	for _, member := range graph.Members {
		if member.CandidateDeploymentID == "" {
			continue
		}
		hasCandidates = true
		if activeTargets[member.Resource] != member.CandidateDeploymentID {
			activatedCandidates = false
		}
	}
	activatedCandidates = activatedCandidates && hasCandidates
	captures := map[string]EnvironmentQualificationSnapshotReceipt{}
	restores := map[string]EnvironmentQualificationRestoreReceipt{}
	smokes := map[string]EnvironmentQualificationSmokeReceipt{}
	jobSmokes := map[string]EnvironmentQualificationJobSmokeReceipt{}
	configs := map[string]EnvironmentQualificationConfigReceipt{}
	frameworkReady := map[string]EnvironmentQualificationFrameworkReadyReceipt{}
	for _, request := range memory.qualifications {
		if request.GraphID != graph.ID {
			continue
		}
		if activatedCandidates {
			deployment, exists := m.deployments[request.DeploymentID]
			frozen, err := deployment.ScopedWorkloadRuntime()
			memberMatches := false
			for _, member := range graph.Members {
				if member.Resource == request.Resource && member.AppID == request.AppID && member.CandidateDeploymentID == request.DeploymentID {
					memberMatches = true
					break
				}
			}
			if !exists || !memberMatches || deployment.Status != DeployLive || deployment.EnvironmentWorkloadHeld() ||
				deployment.AppID != request.AppID || environmentWorkloadArtifact(deployment) != request.Artifact || err != nil || frozen == nil ||
				!frozenCandidateInputsMatch([]byte(deployment.EnvironmentWorkloadRuntime), request.FrozenInputs) {
				continue
			}
		} else if m.qualificationCurrentLocked(memory, request) != nil {
			continue
		}
		if request.ExecutionMode == api.ExecutionModeJob {
			jobSmoke, exists := m.qualificationJobSmokeReceipts[qualificationSmokeReceiptKey(request.ID, request.Attempt)]
			execution, executionExists := m.qualificationExecutions[request.ReservedInstanceID]
			instance, instanceExists := m.instances[request.ReservedInstanceID]
			config, configExists := m.qualificationConfigReceipts[request.ReservedInstanceID]
			if exists && executionExists && instanceExists && execution.RetiredAt != nil && execution.Retirement != nil &&
				execution.DispatchStarted && execution.Retirement.Kind == QualificationNativeRetired &&
				execution.Retirement.ProcessesExited && execution.Retirement.ResourcesRemoved && State(instance.State) == StateStopped &&
				configExists && qualificationConfigReceiptMatchesFrame(config, request, execution.Execution) &&
				qualificationJobSmokeReceiptMatchesRequest(jobSmoke, request) {
				jobSmokes[request.DeploymentID] = jobSmoke
				configs[request.ReservedInstanceID] = config
			}
			continue
		}
		receipt, exists := m.qualificationSnapshots[request.ReservedInstanceID]
		if exists && receipt.Execution.RequestID == request.ID && receipt.Execution.Attempt == request.Attempt && receipt.Execution.Artifact == request.Artifact &&
			m.runtimeConfigInputsFreshLocked(request.AppID, receipt.Inputs) {
			captures[request.DeploymentID] = receipt
			if config, exists := m.qualificationConfigReceipts[request.ReservedInstanceID]; exists {
				configs[request.ReservedInstanceID] = config
			}
			if restore, exists := m.qualificationRestoreReceipts[qualificationRestoreReceiptKey(request.ID, request.Attempt)]; exists &&
				restore.CaptureInstanceID == request.ReservedInstanceID && restore.InstanceID != request.ReservedInstanceID &&
				restore.RequestID == request.ID && restore.Attempt == request.Attempt && qualificationRuntimeValuesEqual(restore.Inputs, receipt.Inputs) &&
				m.runtimeConfigInputsFreshLocked(request.AppID, restore.Inputs) {
				restores[request.ReservedInstanceID] = cloneQualificationRestoreReceipt(restore)
				if config, exists := m.qualificationConfigReceipts[restore.InstanceID]; exists {
					configs[restore.InstanceID] = config
				}
				if smoke, exists := m.qualificationSmokeReceipts[qualificationSmokeReceiptKey(request.ID, request.Attempt)]; exists &&
					qualificationSmokeReceiptMatchesRequest(smoke, request, restore) {
					smokes[request.ReservedInstanceID] = smoke
				}
				if ready, exists := m.qualificationFrameworkReadyReceipts[qualificationSmokeReceiptKey(request.ID, request.Attempt)]; exists &&
					qualificationFrameworkReadyReceiptMatchesRestore(ready, restore, graph.ID) {
					frameworkReady[request.ReservedInstanceID] = ready
				}
			}
		}
	}
	evidence := graphActivationEvidenceWithReleaseTargets(graph, captures, restores, smokes, configs, frameworkReady, jobSmokes, activeTargets)
	jobStatus := "paused"
	if evidence.Activated {
		jobStatus = "active"
	}
	if !m.environmentGitOpsScheduledJobsReadyLocked(memory, graph, jobStatus) {
		evidence.BlockingReasons = append(evidence.BlockingReasons, "environment_scheduled_job_materialization_unready")
		slices.Sort(evidence.BlockingReasons)
		evidence.BlockingReasons = slices.Compact(evidence.BlockingReasons)
	}
	releaseID := m.activeProjectReleaseSets[releaseKey(memory.source.ProjectID, memory.source.EnvironmentSlug)]
	receipt, hasReceipt := m.environmentWorkloadServingReceipts[graph.ID]
	if hasReceipt {
		release := m.projectReleaseSets[releaseID]
		receipt.ReleaseCreatedAt = release.CreatedAt
		receipt.ExpectedScheduledJobs = environmentGitOpsScheduledJobMembers(graph)
		receipt.ScheduledJobAcks = m.environmentWorkloadScheduledJobAcksLocked(graph, release, activeTargets)
	}
	serving := hasReceipt && receipt.SourceID == graph.SourceID && receipt.SourceGeneration == graph.Generation &&
		receipt.IntentVersion == graph.IntentVersion && receipt.RevisionID == graph.RevisionID && receipt.PlanHash == graph.PlanHash &&
		environmentWorkloadServingReceiptMatches(receipt, graph, releaseID,
			m.environmentServingGatewayNamesLocked(), activeTargets) && m.environmentWorkloadServingWeightsMatchLocked(memory.source, graph, activeTargets)
	applyEnvironmentWorkloadServingEvidence(&evidence, serving)
	return evidence
}

func (m *MemStore) environmentWorkloadServingWeightsMatchLocked(source EnvironmentGitSource, graph EnvironmentWorkloadGraph, targets map[string]string) bool {
	if !environmentGraphSupportsProductionServing(graph) {
		return false
	}
	for _, member := range graph.Members {
		targetID := targets[member.Resource]
		if targetID == "" {
			return false
		}
		target, ok := m.deployments[targetID]
		if !ok || target.Status != DeployLive || target.EnvironmentWorkloadHeld() {
			return false
		}
		if member.ExecutionMode == api.ExecutionModeWorker {
			continue
		}
		if member.CandidateDeploymentID != "" {
			if target.TrafficPercent != 100 {
				return false
			}
			for _, deployment := range m.deployments {
				if deployment.AppID == member.AppID && normalizedDeploymentScope(deployment.Scope) == normalizedDeploymentScope(source.EnvironmentSlug) &&
					deployment.Status == DeployLive && deployment.ID != targetID && deployment.TrafficPercent != 0 {
					return false
				}
			}
		} else if target.TrafficPercent <= 0 {
			return false
		}
	}
	return true
}

func (m *MemStore) environmentGraphActiveReleaseTargetsLocked(source EnvironmentGitSource, graph EnvironmentWorkloadGraph) map[string]string {
	releaseID := m.activeProjectReleaseSets[releaseKey(source.ProjectID, source.EnvironmentSlug)]
	release, exists := m.projectReleaseSets[releaseID]
	if !exists || !release.Active || release.ProjectID != source.ProjectID ||
		normalizedDeploymentScope(release.EnvironmentSlug) != normalizedDeploymentScope(source.EnvironmentSlug) {
		return map[string]string{}
	}
	targets := make(map[string]string)
	for _, member := range graph.Members {
		deploymentID := releaseMemberForApp(release, member.AppID)
		if deploymentID == "" || member.CandidateDeploymentID != "" && deploymentID != member.CandidateDeploymentID ||
			member.CandidateDeploymentID == "" && !slices.Contains(member.RetainedDeployments, deploymentID) {
			continue
		}
		deployment, exists := m.deployments[deploymentID]
		if exists && deployment.AppID == member.AppID && deployment.Status == DeployLive &&
			normalizedDeploymentScope(deployment.Scope) == normalizedDeploymentScope(source.EnvironmentSlug) && !deployment.EnvironmentWorkloadHeld() {
			targets[member.Resource] = deploymentID
		}
	}
	return targets
}
