// Package diagnostics explains recorded GitOps status without performing work.
package diagnostics

import (
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Assess uses the caller's observation time so the API, dashboard and CLI share
// the same freshness and evidence rules. Historical applied IDs never establish
// current serving, and an unavailable graph never becomes vacuously ready.
func Assess(status api.EnvironmentGitOpsStatusResponse, now time.Time) api.EnvironmentGitOpsExplanation {
	source := status.Source
	result := api.EnvironmentGitOpsExplanation{ReadOnly: true, AssessedAt: now.UTC(), Status: "unknown", Summary: "Current workload progress is unknown.",
		SourceID: source.ID, Generation: source.Generation, IntentVersion: source.IntentVersion,
		ApprovedRevisionID: source.ApprovedRevisionID, AppliedRevisionID: source.AppliedRevisionID,
		ObservedCommitSHA: source.SourceCommitSHA, SourceFreshness: sourceFreshness(source, now),
		Issues: []api.EnvironmentGitOpsDiagnostic{}, Workloads: []api.EnvironmentWorkloadProgress{}}
	add := func(resource, code string) { result.Issues = append(result.Issues, Explain(resource, code)) }
	if source.ID == "" {
		add("", "source_binding_unknown")
		return result
	}
	if source.Detached || source.Suspended {
		code := "source_suspended"
		if source.Detached {
			code = "source_detached"
		}
		add("", code)
		result.Status, result.Summary = "blocked", "Source reconciliation is stopped. Existing serving state is not reassessed."
		return result
	}
	if result.SourceFreshness != "fresh" {
		add("", "source_"+result.SourceFreshness)
	}
	if source.SourceErrorCode != "" {
		add("", source.SourceErrorCode)
	}
	if source.ApprovedRevisionID == "" {
		add("", "approval_missing")
		result.Status, result.Summary = "blocked", "No immutable revision has been approved."
		return result
	}
	if source.Spec.Mode == "report" {
		add("", "report_mode")
	}
	evidence := status.WorkloadEvidence
	if evidence == nil {
		add("", "current_graph_missing")
	} else if !currentEvidence(source, *evidence) {
		add("", "current_graph_stale")
	} else {
		result.Workloads = slices.Clone(evidence.Workloads)
		if result.Workloads == nil {
			result.Workloads = []api.EnvironmentWorkloadProgress{}
		}
		for i := range result.Workloads {
			codes := make([]string, 0, len(result.Workloads[i].BlockingReasons))
			for _, code := range result.Workloads[i].BlockingReasons {
				codes = append(codes, Explain(result.Workloads[i].Resource, code).Code)
			}
			result.Workloads[i].BlockingReasons = codes
		}
		for _, code := range evidence.BlockingReasons {
			add("", code)
		}
		if evidence.GraphErrorCode != "" {
			add("", evidence.GraphErrorCode)
		}
		for _, workload := range result.Workloads {
			for _, code := range workload.BlockingReasons {
				add(workload.Resource, code)
			}
			if workload.ExecutionMode == api.ExecutionModeJob && workload.Qualification == "complete" && workload.Activation == "complete" && workload.Serving != "complete" {
				add(workload.Resource, "scheduled_occurrence_unproven")
			}
		}
		result.Status, result.Summary = "progressing", "The approved revision is awaiting current qualification, activation or serving evidence."
		if len(evidence.BlockingReasons) > 0 {
			result.Status = "blocked"
		}
		if evidence.Serving && evidence.Activated && evidence.Qualified && evidence.ArtifactsPrepared {
			result.ServingRevisionID = evidence.RevisionID
			result.Status, result.Summary = "serving", "The current approved graph has qualification, activation and serving evidence."
			if result.SourceFreshness != "fresh" || source.SourceErrorCode != "" {
				result.Status, result.Summary = "degraded", "The approved graph has serving evidence; Git source checks need attention."
			}
		}
	}
	if source.Spec.Mode == "report" {
		result.Status, result.Summary = "reporting", "Report mode records drift against the approved revision; it does not apply changes."
	}
	return result
}

func currentEvidence(source api.EnvironmentGitSource, evidence api.EnvironmentWorkloadActivationEvidence) bool {
	if evidence.Serving && (!evidence.Qualified || !evidence.Activated || !evidence.ArtifactsPrepared) {
		return false
	}
	if evidence.Activated && !evidence.Qualified {
		return false
	}
	for _, workload := range evidence.Workloads {
		if evidence.Qualified && workload.Preparation != "complete" ||
			evidence.Serving && (workload.Qualification != "complete" || workload.Activation != "complete" || workload.Serving != "complete") {
			return false
		}
	}
	return evidence.GraphID != "" && evidence.SourceID == source.ID && evidence.EnvironmentID == source.EnvironmentID &&
		evidence.RevisionID == source.ApprovedRevisionID && evidence.Generation == source.Generation && evidence.IntentVersion == source.IntentVersion
}

func sourceFreshness(source api.EnvironmentGitSource, now time.Time) string {
	if source.SourceCheckedAt == nil || source.SourceVerifiedAt == nil || source.SourceCheckedAt.After(now) || source.SourceVerifiedAt.After(now) {
		return "unknown"
	}
	cutoff := now.Add(-api.EnvironmentGitSourceStaleAfter)
	if !source.SourceCheckedAt.After(cutoff) || !source.SourceVerifiedAt.After(cutoff) {
		return "stale"
	}
	return "fresh"
}
