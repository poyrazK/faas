package diagnostics

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func diagnosticStatus(now time.Time) api.EnvironmentGitOpsStatusResponse {
	return api.EnvironmentGitOpsStatusResponse{Source: api.EnvironmentGitSource{ID: "source", EnvironmentID: "environment", Generation: 3, IntentVersion: 5,
		ApprovedRevisionID: "approved", AppliedRevisionID: "historical", SourceCheckedAt: &now, SourceVerifiedAt: &now,
		SourceCommitSHA: strings.Repeat("a", 40), Spec: api.EnvironmentGitSourceSpec{Mode: "enforce"}},
		WorkloadEvidence: &api.EnvironmentWorkloadActivationEvidence{GraphID: "graph", SourceID: "source", EnvironmentID: "environment", RevisionID: "approved",
			Generation: 3, IntentVersion: 5, ArtifactsPrepared: true, Qualified: true, Activated: true,
			Workloads: []api.EnvironmentWorkloadProgress{{Resource: "workload/report", ExecutionMode: "job", Preparation: "complete", Qualification: "complete", Activation: "complete", Serving: "unknown"}}}}
}

func TestExplanationSeparatesAppliedActivationAndServing(t *testing.T) {
	now := time.Now().UTC()
	status := diagnosticStatus(now)
	explanation := Assess(status, now)
	if !explanation.ReadOnly || explanation.AppliedRevisionID != "historical" || explanation.ServingRevisionID != "" || explanation.Workloads[0].Serving != "unknown" {
		t.Fatalf("historical or activated facts were promoted to serving: %+v", explanation)
	}
	found := false
	for _, issue := range explanation.Issues {
		found = found || issue.Resource == "workload/report" && issue.Code == "scheduled_occurrence_unproven" && issue.Actor == "customer"
	}
	if !found {
		t.Fatal("missing workload-scoped scheduled occurrence guidance")
	}
	status.WorkloadEvidence.Serving = true
	status.WorkloadEvidence.Workloads[0].Serving = "complete"
	if result := Assess(status, now); result.ServingRevisionID != "approved" || result.Status != "serving" {
		t.Fatalf("valid serving proof lost: %+v", result)
	}
}

func TestExplanationRejectsStaleAndContradictoryEvidence(t *testing.T) {
	now := time.Now().UTC()
	for name, mutate := range map[string]func(*api.EnvironmentWorkloadActivationEvidence){
		"source":       func(e *api.EnvironmentWorkloadActivationEvidence) { e.SourceID = "other" },
		"environment":  func(e *api.EnvironmentWorkloadActivationEvidence) { e.EnvironmentID = "other" },
		"revision":     func(e *api.EnvironmentWorkloadActivationEvidence) { e.RevisionID = "old" },
		"generation":   func(e *api.EnvironmentWorkloadActivationEvidence) { e.Generation-- },
		"intent":       func(e *api.EnvironmentWorkloadActivationEvidence) { e.IntentVersion-- },
		"unqualified":  func(e *api.EnvironmentWorkloadActivationEvidence) { e.Qualified = false; e.Serving = true },
		"failed build": func(e *api.EnvironmentWorkloadActivationEvidence) { e.Workloads[0].Preparation = "failed" },
	} {
		t.Run(name, func(t *testing.T) {
			status := diagnosticStatus(now)
			mutate(status.WorkloadEvidence)
			result := Assess(status, now)
			if result.Status != "unknown" || result.ServingRevisionID != "" || len(result.Workloads) != 0 {
				t.Fatalf("stale evidence exposed: %+v", result)
			}
		})
	}
}

func TestExplanationSourceFreshnessAndOutageDoNotEraseServingFacts(t *testing.T) {
	now := time.Now().UTC()
	old, future := now.Add(-api.EnvironmentGitSourceStaleAfter), now.Add(time.Second)
	for _, check := range []struct {
		at   *time.Time
		want string
	}{{nil, "unknown"}, {&old, "stale"}, {&future, "unknown"}, {&now, "fresh"}} {
		status := diagnosticStatus(now)
		status.Source.SourceCheckedAt = check.at
		status.WorkloadEvidence.Serving = true
		status.WorkloadEvidence.Workloads[0].Serving = "complete"
		result := Assess(status, now)
		if result.SourceFreshness != check.want || result.ServingRevisionID != "approved" {
			t.Fatalf("source check changed serving fact: %+v", result)
		}
	}
	status := diagnosticStatus(now)
	status.Source.SourceErrorCode = "environment_git_repository_unavailable"
	status.WorkloadEvidence.Serving = true
	status.WorkloadEvidence.Workloads[0].Serving = "complete"
	if result := Assess(status, now); result.Status != "degraded" || result.ServingRevisionID != "approved" {
		t.Fatalf("outage erased serving evidence: %+v", result)
	}
}

func TestExplanationReportModeAndMissingEvidenceRemainExplicit(t *testing.T) {
	now := time.Now().UTC()
	status := diagnosticStatus(now)
	status.Source.Spec.Mode = "report"
	status.WorkloadEvidence = nil
	result := Assess(status, now)
	if result.Status != "reporting" || result.ServingRevisionID != "" || !strings.Contains(result.Summary, "does not apply") {
		t.Fatalf("report mode implied deployment: %+v", result)
	}
	status.Source.Spec.Mode = "enforce"
	if result := Assess(status, now); result.Status != "unknown" || result.ServingRevisionID != "" {
		t.Fatalf("missing graph guessed ready: %+v", result)
	}
	status.Source.Suspended = true
	if result := Assess(status, now); result.Status != "blocked" || len(result.Workloads) != 0 {
		t.Fatalf("suspension exposed active progress: %+v", result)
	}
}

func TestExplanationRedactsUnknownErrorsAndDoesNotAliasInputs(t *testing.T) {
	now := time.Now().UTC()
	status := diagnosticStatus(now)
	status.WorkloadEvidence.GraphErrorCode = "credential=PRIVATE-do-not-copy"
	status.WorkloadEvidence.Workloads[0].BlockingReasons = []string{"environment_serving_evidence_missing", "PRIVATE-scoped-error"}
	result := Assess(status, now)
	encoded, err := json.Marshal(result)
	if err != nil || strings.Contains(string(encoded), "PRIVATE") {
		t.Fatalf("upstream error leaked: %s %v", encoded, err)
	}
	result.Workloads[0].BlockingReasons[0] = "changed"
	if status.WorkloadEvidence.Workloads[0].BlockingReasons[0] != "environment_serving_evidence_missing" {
		t.Fatal("assessment mutated its input")
	}
}
