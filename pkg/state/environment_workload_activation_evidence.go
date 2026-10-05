package state

import (
	"context"
	"slices"

	"github.com/onebox-faas/faas/pkg/environmentsync"
)

// Evidence is an assessment of committed facts, not an activation capability.
// Warm capture alone cannot satisfy isolated smoke, restored readiness, whole
// graph publication, or serving convergence. Those receipts remain mandatory.
type EnvironmentWorkloadActivationEvidence struct {
	GraphID           string   `json:"graph_id"`
	RevisionID        string   `json:"revision_id"`
	Generation        int64    `json:"generation"`
	PlanHash          string   `json:"plan_hash"`
	ArtifactsPrepared bool     `json:"artifacts_prepared"`
	Candidates        int      `json:"candidates"`
	CapturesRecorded  int      `json:"captures_recorded"`
	Qualified         bool     `json:"qualified"`
	Activated         bool     `json:"activated"`
	Serving           bool     `json:"serving"`
	BlockingReasons   []string `json:"blocking_reasons"`
}

type EnvironmentGitOpsActivationEvidenceStore interface {
	EnvironmentGitOpsActivationEvidence(context.Context, EnvironmentGitOpsLease, environmentsync.Plan) (EnvironmentWorkloadActivationEvidence, error)
}

func graphActivationEvidence(graph EnvironmentWorkloadGraph, captures map[string]EnvironmentQualificationSnapshotReceipt) EnvironmentWorkloadActivationEvidence {
	evidence := EnvironmentWorkloadActivationEvidence{GraphID: graph.ID, RevisionID: graph.RevisionID, Generation: graph.Generation, PlanHash: graph.PlanHash,
		ArtifactsPrepared: graph.Phase == "prepared", BlockingReasons: []string{"environment_isolated_smoke_evidence_missing", "environment_restore_evidence_missing", "environment_activation_evidence_missing", "environment_serving_evidence_missing"}}
	if !evidence.ArtifactsPrepared {
		evidence.BlockingReasons = append(evidence.BlockingReasons, "environment_artifact_preparation_incomplete")
	}
	for _, member := range graph.Members {
		if member.CandidateDeploymentID == "" {
			evidence.BlockingReasons = append(evidence.BlockingReasons, "environment_retained_workload_evidence_missing")
			continue
		}
		evidence.Candidates++
		receipt, exists := captures[member.CandidateDeploymentID]
		frame := receipt.Execution
		if exists && frame.GraphID == graph.ID && frame.SourceID == graph.SourceID && frame.EnvironmentID == graph.EnvironmentID && frame.RevisionID == graph.RevisionID &&
			frame.Generation == graph.Generation && frame.IntentVersion == graph.IntentVersion && frame.PlanHash == graph.PlanHash && frame.Resource == member.Resource &&
			frame.AppID == member.AppID && frame.DeploymentID == member.CandidateDeploymentID && ValidateEnvironmentQualificationSnapshot(frame, receipt.Snapshot) == nil {
			evidence.CapturesRecorded++
		}
	}
	if evidence.CapturesRecorded != evidence.Candidates {
		evidence.BlockingReasons = append(evidence.BlockingReasons, "environment_capture_evidence_missing")
	}
	slices.Sort(evidence.BlockingReasons)
	evidence.BlockingReasons = slices.Compact(evidence.BlockingReasons)
	return evidence
}
