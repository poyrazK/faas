package state

import (
	"context"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
)

// Evidence is an assessment of committed facts, not an activation capability.
// Unchanged workloads count only when their exact live deployment is still in
// the active release set. Warm capture alone cannot satisfy isolated smoke,
// restored readiness, whole graph publication, or serving convergence.
type EnvironmentWorkloadActivationEvidence = api.EnvironmentWorkloadActivationEvidence

type EnvironmentGitOpsActivationEvidenceStore interface {
	EnvironmentGitOpsActivationEvidence(context.Context, EnvironmentGitOpsLease, environmentsync.Plan) (EnvironmentWorkloadActivationEvidence, error)
}

// EnvironmentGitOpsCurrentWorkloadEvidenceStore serves a read-only view of
// evidence for the source's current approved plan. A nil result means that no
// current candidate graph exists; stale graphs are never surfaced as current.
type EnvironmentGitOpsCurrentWorkloadEvidenceStore interface {
	CurrentEnvironmentGitOpsWorkloadEvidence(context.Context, string, string) (*EnvironmentWorkloadActivationEvidence, error)
}

func graphActivationEvidence(graph EnvironmentWorkloadGraph, captures map[string]EnvironmentQualificationSnapshotReceipt,
	restores map[string]EnvironmentQualificationRestoreReceipt, smokes map[string]EnvironmentQualificationSmokeReceipt,
	configs map[string]EnvironmentQualificationConfigReceipt,
	frameworkReady map[string]EnvironmentQualificationFrameworkReadyReceipt,
	jobSmokeSets ...map[string]EnvironmentQualificationJobSmokeReceipt) EnvironmentWorkloadActivationEvidence {
	var jobSmokes map[string]EnvironmentQualificationJobSmokeReceipt
	if len(jobSmokeSets) > 0 {
		jobSmokes = jobSmokeSets[0]
	}
	return graphActivationEvidenceWithReleaseTargets(graph, captures, restores, smokes, configs, frameworkReady, jobSmokes, nil)
}

func graphActivationEvidenceWithReleaseTargets(graph EnvironmentWorkloadGraph, captures map[string]EnvironmentQualificationSnapshotReceipt,
	restores map[string]EnvironmentQualificationRestoreReceipt, smokes map[string]EnvironmentQualificationSmokeReceipt,
	configs map[string]EnvironmentQualificationConfigReceipt,
	frameworkReady map[string]EnvironmentQualificationFrameworkReadyReceipt,
	jobSmokes map[string]EnvironmentQualificationJobSmokeReceipt,
	activeReleaseTargets map[string]string) EnvironmentWorkloadActivationEvidence {
	evidence := EnvironmentWorkloadActivationEvidence{GraphID: graph.ID, SourceID: graph.SourceID, EnvironmentID: graph.EnvironmentID,
		RevisionID: graph.RevisionID, DefinitionDigest: graph.DefinitionDigest, Generation: graph.Generation, IntentVersion: graph.IntentVersion,
		PlanHash: graph.PlanHash, GraphPhase: graph.Phase, GraphErrorCode: graph.ErrorCode,
		ArtifactsPrepared: graph.Phase == "prepared", BlockingReasons: []string{}}
	if !evidence.ArtifactsPrepared {
		evidence.BlockingReasons = append(evidence.BlockingReasons, "environment_artifact_preparation_incomplete")
	}
	if len(graph.Members) == 0 {
		// An empty graph is not a vacuously qualified workload cohort. Keep
		// status fail-closed when a definition has no activatable workloads.
		evidence.BlockingReasons = append(evidence.BlockingReasons, "environment_workload_graph_empty")
	}
	nonJobCandidates, jobCandidates := 0, 0
	for _, member := range graph.Members {
		switch member.ExecutionMode {
		case api.ExecutionModeRequest, api.ExecutionModeService, api.ExecutionModeWorker, api.ExecutionModeJob:
		default:
			evidence.BlockingReasons = append(evidence.BlockingReasons, "environment_workload_execution_mode_unsupported")
		}
		for name, mode := range member.QueueModes {
			if !api.ValidQueueBindingName(name) || mode != "push" && mode != "pull" {
				evidence.BlockingReasons = append(evidence.BlockingReasons, "environment_queue_binding_mode_unsupported")
			}
		}
		// Retained HTTP and worker workloads already belong to the current
		// release. Candidate-only smoke adapters must not block an unchanged
		// member, but a job still requires its one-shot execution receipt below.
		if member.CandidateDeploymentID == "" && member.ExecutionMode != api.ExecutionModeJob {
			deploymentID := activeReleaseTargets[member.Resource]
			if deploymentID == "" || graph.ResourceIDs[member.Resource] != member.AppID || !slices.Contains(member.RetainedDeployments, deploymentID) {
				evidence.BlockingReasons = append(evidence.BlockingReasons, "environment_retained_workload_evidence_missing")
				continue
			}
			evidence.RetainedWorkloadsRecorded++
			continue
		}
		functionHTTP := member.Function && member.ExecutionMode == api.ExecutionModeRequest
		if member.ExecutionMode == api.ExecutionModeJob {
			if member.JobSmokeConfigured {
				jobCandidates++
			} else {
				evidence.BlockingReasons = append(evidence.BlockingReasons, "environment_job_execution_smoke_contract_missing")
			}
			if member.QueueBindingsConfigured || len(member.QueueModes) != 0 {
				evidence.BlockingReasons = append(evidence.BlockingReasons, "environment_job_queue_binding_execution_unsupported")
			}
		}
		if member.ExecutionMode != api.ExecutionModeWorker && member.ExecutionMode != api.ExecutionModeJob && !functionHTTP && len(member.QueueModes) != 0 {
			// HTTP health checks cannot stand in for an unsupported queue adapter.
			evidence.BlockingReasons = append(evidence.BlockingReasons, "environment_queue_binding_execution_unsupported")
		}
		if member.CandidateDeploymentID == "" {
			deploymentID := activeReleaseTargets[member.Resource]
			if deploymentID == "" || graph.ResourceIDs[member.Resource] != member.AppID || !slices.Contains(member.RetainedDeployments, deploymentID) {
				evidence.BlockingReasons = append(evidence.BlockingReasons, "environment_retained_workload_evidence_missing")
				continue
			}
			evidence.RetainedWorkloadsRecorded++
			continue
		}
		evidence.Candidates++
		if member.ExecutionMode == api.ExecutionModeJob {
			receipt, receiptExists := jobSmokes[member.CandidateDeploymentID]
			config, configExists := configs[receipt.InstanceID]
			request := EnvironmentWorkloadQualificationRequest{ID: receipt.RequestID, GraphID: receipt.GraphID, Attempt: receipt.Attempt}
			configValid := configExists && qualificationConfigReceiptMatchesAttempt(config, request, receipt.InstanceID, "")
			if member.JobSmokeConfigured && receiptExists && receipt.GraphID == graph.ID && receipt.Resource == member.Resource &&
				receipt.InstanceID != "" && receipt.Attempt > 0 && receipt.PolicyID == environmentQualificationJobSmokePolicyID &&
				receipt.PolicySHA256 != "" && receipt.ResultSHA256 == qualificationJobSmokeResultSHA256(0, "succeeded", 0) && configValid {
				evidence.JobSmokesRecorded++
				evidence.SmokesRecorded++
				evidence.GuestConfigAcknowledgementsRecorded++
			}
			continue
		}
		nonJobCandidates++
		workerSmokeRecorded := false
		receipt, exists := captures[member.CandidateDeploymentID]
		frame := receipt.Execution
		captureValid := exists && frame.GraphID == graph.ID && frame.SourceID == graph.SourceID && frame.EnvironmentID == graph.EnvironmentID && frame.RevisionID == graph.RevisionID &&
			frame.Generation == graph.Generation && frame.IntentVersion == graph.IntentVersion && frame.PlanHash == graph.PlanHash && frame.Resource == member.Resource &&
			frame.AppID == member.AppID && frame.DeploymentID == member.CandidateDeploymentID && ValidateEnvironmentQualificationSnapshot(frame, receipt.Snapshot) == nil
		if captureValid {
			evidence.CapturesRecorded++
			captureConfig, captureConfigExists := configs[frame.InstanceID]
			captureConfigValid := captureConfigExists && qualificationConfigReceiptMatchesAttempt(captureConfig,
				EnvironmentWorkloadQualificationRequest{ID: frame.RequestID, GraphID: frame.GraphID, Attempt: frame.Attempt}, frame.InstanceID, "")
			restore, restoreExists := restores[frame.InstanceID]
			if restoreExists && restore.RequestID == frame.RequestID && restore.Attempt == frame.Attempt && restore.CaptureInstanceID == frame.InstanceID &&
				restore.InstanceID != "" && restore.InstanceID != frame.InstanceID && qualificationRuntimeValuesEqual(restore.Inputs, receipt.Inputs) {
				evidence.RestoresRecorded++
				restoredConfig, restoredConfigExists := configs[restore.InstanceID]
				if captureConfigValid && restoredConfigExists && qualificationConfigReceiptMatchesAttempt(restoredConfig,
					EnvironmentWorkloadQualificationRequest{ID: restore.RequestID, GraphID: frame.GraphID, Attempt: restore.Attempt},
					restore.InstanceID, frame.InstanceID) {
					evidence.GuestConfigAcknowledgementsRecorded++
				}
				if ready, readyExists := frameworkReady[frame.InstanceID]; readyExists && qualificationFrameworkReadyReceiptMatchesRestore(ready, restore, graph.ID) {
					evidence.FrameworkReadyAcknowledgementsRecorded++
				}
				smoke, smokeExists := smokes[frame.InstanceID]
				if smokeExists && qualificationSmokeReceiptMatches(smoke, restore) {
					evidence.SmokesRecorded++
					workerSmokeRecorded = true
				}
			}
		}
		if member.ExecutionMode == api.ExecutionModeWorker &&
			(graphMemberHasQueueMode(member, "push") || graphMemberHasQueueMode(member, "pull") || len(member.QueueModes) == 0) && !workerSmokeRecorded {
			evidence.BlockingReasons = append(evidence.BlockingReasons, "environment_worker_queue_smoke_contract_missing")
		}
		if functionHTTP && graphMemberHasQueueMode(member, "push") && !workerSmokeRecorded {
			evidence.BlockingReasons = append(evidence.BlockingReasons, "environment_function_queue_smoke_evidence_missing")
		}
	}
	if evidence.JobSmokesRecorded != jobCandidates {
		evidence.BlockingReasons = append(evidence.BlockingReasons, "environment_job_execution_smoke_evidence_missing")
	}
	if nonJobCandidates > 0 && evidence.CapturesRecorded != nonJobCandidates {
		evidence.BlockingReasons = append(evidence.BlockingReasons, "environment_capture_evidence_missing")
	}
	if nonJobCandidates > 0 && evidence.RestoresRecorded != nonJobCandidates {
		evidence.BlockingReasons = append(evidence.BlockingReasons, "environment_restore_evidence_missing")
	}
	if nonJobCandidates > 0 && evidence.GuestConfigAcknowledgementsRecorded != nonJobCandidates {
		evidence.BlockingReasons = append(evidence.BlockingReasons, "environment_guest_config_acknowledgement_missing")
	}
	if nonJobCandidates > 0 && evidence.FrameworkReadyAcknowledgementsRecorded != nonJobCandidates {
		evidence.BlockingReasons = append(evidence.BlockingReasons, "environment_framework_ready_evidence_missing")
	}
	if evidence.SmokesRecorded != evidence.Candidates {
		evidence.BlockingReasons = append(evidence.BlockingReasons, "environment_isolated_smoke_evidence_missing")
	}
	// Qualification, activation, and serving are distinct facts. An exact
	// active release-set match can report activation, but cannot satisfy serving
	// convergence without a separately committed serving receipt.
	evidence.Qualified = evidence.ArtifactsPrepared && len(evidence.BlockingReasons) == 0
	if !environmentGraphSupportsProductionServing(graph) {
		// Isolated qualification is not proof that a production execution
		// adapter exists. Keep the broad blocker for older clients and add a
		// mode-specific reason so operators can see which adapter is missing.
		evidence.BlockingReasons = append(evidence.BlockingReasons, "environment_workload_production_execution_adapter_missing")
		evidence.BlockingReasons = append(evidence.BlockingReasons, environmentGraphProductionAdapterBlockers(graph)...)
	}
	evidence.Activated = graphMatchesActiveReleaseSet(graph, activeReleaseTargets)
	if evidence.Activated && !evidence.Qualified {
		// Preserve the evidence that failed qualification while making an
		// already-published unqualified graph visible as an ordering violation.
		evidence.BlockingReasons = append(evidence.BlockingReasons, "environment_activation_before_qualification")
	}
	if !evidence.Activated {
		evidence.BlockingReasons = append(evidence.BlockingReasons, "environment_activation_evidence_missing")
	}
	if !evidence.Serving {
		evidence.BlockingReasons = append(evidence.BlockingReasons, "environment_serving_evidence_missing")
	}
	slices.Sort(evidence.BlockingReasons)
	evidence.BlockingReasons = slices.Compact(evidence.BlockingReasons)
	return evidence
}

func environmentGraphProductionAdapterBlockers(graph EnvironmentWorkloadGraph) []string {
	blockers := make([]string, 0)
	hasServiceBindings := false
	for _, member := range graph.Members {
		hasServiceBindings = hasServiceBindings || member.ServiceBindingsConfigured || len(member.ServiceBindings) != 0
		switch member.ExecutionMode {
		case api.ExecutionModeJob:
			if !member.ScheduleConfigured {
				blockers = append(blockers, "environment_job_production_adapter_missing")
			} else if member.JobID == "" {
				blockers = append(blockers, "environment_scheduled_job_dispatch_adapter_missing")
			} else if !member.JobSmokeConfigured {
				blockers = append(blockers, "environment_job_execution_smoke_contract_missing")
			} else if member.QueueBindingsConfigured || len(member.QueueModes) != 0 || len(member.QueueBindings) != 0 {
				blockers = append(blockers, "environment_job_queue_binding_execution_unsupported")
			}
		case api.ExecutionModeWorker:
		}
	}
	if hasServiceBindings && !environmentGraphServiceBindingsSupported(graph, nil) {
		blockers = append(blockers, "environment_service_binding_contract_unsupported")
	}
	return blockers
}

func graphMatchesActiveReleaseSet(graph EnvironmentWorkloadGraph, activeReleaseTargets map[string]string) bool {
	if graph.Phase != "prepared" || len(graph.Members) == 0 || len(activeReleaseTargets) < len(graph.Members) {
		return false
	}
	seenResources := make(map[string]struct{}, len(graph.Members))
	for _, member := range graph.Members {
		if member.Resource == "" || member.AppID == "" || graph.ResourceIDs[member.Resource] != member.AppID {
			return false
		}
		if _, duplicate := seenResources[member.Resource]; duplicate {
			return false
		}
		seenResources[member.Resource] = struct{}{}
		deploymentID := activeReleaseTargets[member.Resource]
		if deploymentID == "" {
			return false
		}
		if member.CandidateDeploymentID != "" {
			if deploymentID != member.CandidateDeploymentID {
				return false
			}
		} else if !slices.Contains(member.RetainedDeployments, deploymentID) {
			return false
		}
	}
	return true
}

func graphMemberHasQueueMode(member EnvironmentWorkloadGraphMember, mode string) bool {
	for _, queueMode := range member.QueueModes {
		if queueMode == mode {
			return true
		}
	}
	return false
}

func applyEnvironmentWorkloadServingEvidence(evidence *EnvironmentWorkloadActivationEvidence, serving bool) {
	evidence.Serving = serving
	evidence.BlockingReasons = slices.DeleteFunc(evidence.BlockingReasons, func(reason string) bool {
		return reason == "environment_serving_evidence_missing"
	})
	if !serving {
		evidence.BlockingReasons = append(evidence.BlockingReasons, "environment_serving_evidence_missing")
	}
	slices.Sort(evidence.BlockingReasons)
	evidence.BlockingReasons = slices.Compact(evidence.BlockingReasons)
}
