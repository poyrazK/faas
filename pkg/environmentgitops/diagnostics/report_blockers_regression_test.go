package diagnostics

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestReviewExplainMustIncludeCurrentReportBlockers(t *testing.T) {
	now := time.Now().UTC()
	status := diagnosticStatus(now)
	status.Source.Spec.Mode = "report"
	status.WorkloadEvidence = nil
	empty := Assess(status, now)
	raw, err := json.Marshal(api.EnvironmentGitOpsPlan{Manager: status.Source.ID, Revision: status.Source.ApprovedRevisionID,
		Generation: status.Source.Generation, ObservedVersion: status.Source.IntentVersion, Hash: strings.Repeat("a", 64),
		BlockingReasons: []string{"workload/api#variables/MODE: adoption required"},
		Changes:         []api.EnvironmentGitOpsChange{{Resource: "workload/api", Path: "variables/MODE", Action: "conflict", Reason: "existing field requires reviewed adoption"}}})
	if err != nil {
		t.Fatal(err)
	}
	status.Runs = []api.EnvironmentGitOpsRun{{ID: "run", SourceID: status.Source.ID, RevisionID: status.Source.ApprovedRevisionID,
		Generation: status.Source.Generation, Status: "blocked", Plan: raw, StartedAt: now.Add(-time.Second), CompletedAt: &now}}
	blocked := Assess(status, now)
	if reflect.DeepEqual(empty, blocked) {
		t.Fatalf("explanation identical with no reports and a current adoption blocker: %+v", blocked)
	}
	if blocked.Status != "blocked" || blocked.ReportFreshness != "fresh" || blocked.ReportID != "run" {
		t.Fatalf("current report identity or outcome missing: %+v", blocked)
	}
	found := false
	for _, issue := range blocked.Issues {
		found = found || issue.Code == "environment_adoption_required" && issue.Resource == "workload/api"
	}
	if !found {
		t.Fatal("missing safe adoption guidance")
	}
	for _, tc := range []struct {
		name   string
		mutate func(*api.EnvironmentGitOpsStatusResponse)
	}{
		{"intent changed", func(s *api.EnvironmentGitOpsStatusResponse) { s.Source.IntentVersion++ }},
		{"generation changed", func(s *api.EnvironmentGitOpsStatusResponse) { s.Source.Generation++ }},
		{"revision changed", func(s *api.EnvironmentGitOpsStatusResponse) { s.Source.ApprovedRevisionID = "new" }},
		{"future completion", func(s *api.EnvironmentGitOpsStatusResponse) { at := now.Add(time.Second); s.Runs[0].CompletedAt = &at }},
		{"unfinished", func(s *api.EnvironmentGitOpsStatusResponse) { s.Runs[0].CompletedAt = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copy := status
			copy.Runs = append([]api.EnvironmentGitOpsRun(nil), status.Runs...)
			tc.mutate(&copy)
			result := Assess(copy, now)
			if result.ReportID != "" || result.Status == "blocked" {
				t.Fatalf("historical or incomplete report became current: %+v", result)
			}
		})
	}
	stale := Assess(status, now.Add(4*api.EnvironmentGitOpsReportCheckInterval))
	if stale.ReportFreshness != "stale" || stale.Status == "blocked" {
		t.Fatalf("old report treated as a current blocker: %+v", stale)
	}
}

func TestReportDiagnosticsRedactUnrecognizedDetails(t *testing.T) {
	now := time.Now().UTC()
	status := diagnosticStatus(now)
	status.Source.Spec.Mode, status.WorkloadEvidence = "report", nil
	plan, _ := json.Marshal(api.EnvironmentGitOpsPlan{Manager: status.Source.ID, Revision: status.Source.ApprovedRevisionID,
		Generation: status.Source.Generation, ObservedVersion: status.Source.IntentVersion,
		BlockingReasons: []string{"PRIVATE-resource: credential=PRIVATE", "workload/api: credential=PRIVATE"}})
	status.Runs = []api.EnvironmentGitOpsRun{{ID: "current", SourceID: status.Source.ID, RevisionID: status.Source.ApprovedRevisionID,
		Generation: status.Source.Generation, Status: "failed", ErrorCode: "PRIVATE-error", Plan: plan, StartedAt: now.Add(-time.Second), CompletedAt: &now}}
	result := Assess(status, now)
	raw, err := json.Marshal(result)
	if err != nil || strings.Contains(string(raw), "PRIVATE") || result.ReportStatus != "failed" || result.Status != "blocked" {
		t.Fatalf("unsafe or missing failure diagnostics: %s %v", raw, err)
	}
}

func TestReportOutcomesPreservePublishedExplanationStatusValues(t *testing.T) {
	now := time.Now().UTC()
	status := diagnosticStatus(now)
	status.Source.Spec.Mode, status.WorkloadEvidence = "report", nil
	plan, err := json.Marshal(api.EnvironmentGitOpsPlan{Manager: status.Source.ID, Revision: status.Source.ApprovedRevisionID,
		Generation: status.Source.Generation, ObservedVersion: status.Source.IntentVersion, Hash: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	// Older SDKs validate this published enum. Detailed attempt outcomes belong
	// in the additive report_status field rather than new top-level values.
	allowed := []string{"unknown", "blocked", "progressing", "reporting", "serving", "degraded"}
	for _, outcome := range []string{"blocked", "partial", "failed", "drifted", "overridden", "converged"} {
		t.Run(outcome, func(t *testing.T) {
			status.Runs = []api.EnvironmentGitOpsRun{{ID: "report", SourceID: status.Source.ID, RevisionID: status.Source.ApprovedRevisionID,
				Generation: status.Source.Generation, Status: outcome, Plan: plan, StartedAt: now.Add(-time.Second), CompletedAt: &now}}
			result := Assess(status, now)
			if !slices.Contains(allowed, result.Status) || result.ReportStatus != outcome || result.ReportFreshness != "fresh" {
				t.Fatalf("report outcome broke the public explanation contract: %+v", result)
			}
		})
	}
}
