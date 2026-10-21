package rollout

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func servingStatus(now time.Time) api.EnvironmentGitOpsStatusResponse {
	return api.EnvironmentGitOpsStatusResponse{Source: api.EnvironmentGitSource{
		ID: "source", AccountID: "account", ProjectID: "project", EnvironmentID: "environment", EnvironmentSlug: "production",
		Generation: 3, IntentVersion: 5, ApprovedRevisionID: "revision", AppliedRevisionID: "historical",
		SourceCommitSHA: strings.Repeat("b", 40), SourceCheckedAt: &now, SourceVerifiedAt: &now,
		Spec: api.EnvironmentGitSourceSpec{Provider: "github", Repository: "shop/platform", RepositoryID: 42, InstallationID: 12,
			Ref: "refs/heads/main", ManifestPath: "production.yaml", Mode: "enforce", ApprovalPolicy: "manual"}},
		WorkloadEvidence: &api.EnvironmentWorkloadActivationEvidence{CommitSHA: strings.Repeat("a", 40), GraphID: "graph", SourceID: "source", EnvironmentID: "environment",
			RevisionID: "revision", DefinitionDigest: strings.Repeat("c", 64), PlanHash: strings.Repeat("d", 64), Generation: 3, IntentVersion: 5, GraphPhase: "prepared",
			ArtifactsPrepared: true, Qualified: true, Activated: true, Serving: true, Candidates: 1,
			Workloads: []api.EnvironmentWorkloadProgress{{Resource: "workload/api", AppID: "app", ExecutionMode: "request", Preparation: "complete", Qualification: "complete", Activation: "complete", Serving: "complete"}}}}
}

func newTracker(t *testing.T) *Tracker {
	t.Helper()
	tracker, err := NewTracker(strings.Repeat("a", 40), "production")
	if err != nil {
		t.Fatal(err)
	}
	return tracker
}

func TestWaitObservesExactApprovedCommitRatherThanObservedOrAppliedHistory(t *testing.T) {
	now := time.Now().UTC()
	tracker := newTracker(t)
	status := servingStatus(now)
	status.Source.SourceCommitSHA = strings.Repeat("a", 40)
	status.WorkloadEvidence.CommitSHA = strings.Repeat("b", 40)
	if out, err := tracker.Observe(status, now); err != nil || out.Status != "waiting" || out.ExitCode() == 0 || out.CommitSHA != "" {
		t.Fatal("old serving release satisfied the requested commit", out, err)
	}
	status.WorkloadEvidence.CommitSHA = strings.Repeat("a", 40)
	status.WorkloadEvidence.Serving = false
	status.WorkloadEvidence.Workloads[0].Serving = "unknown"
	if out, err := tracker.Observe(status, now); err != nil || out.Status != "progressing" || out.RevisionID != "revision" {
		t.Fatal(out, err)
	}
	status.WorkloadEvidence.Serving = true
	status.WorkloadEvidence.Workloads[0].Serving = "complete"
	if out, err := tracker.Observe(status, now); err != nil || out.Status != "serving" || out.ExitCode() != 0 || !out.ReadOnly || out.Explanation.AppliedRevisionID != "historical" {
		t.Fatal(out, err)
	}
	if status.Source.AppliedRevisionID != "historical" {
		t.Fatal("wait changed applied history")
	}
}

func TestWaitRejectsIncompleteStaleContradictoryOrInactiveEvidence(t *testing.T) {
	now := time.Now().UTC()
	for name, mutate := range map[string]func(*api.EnvironmentGitOpsStatusResponse){
		"missing member progress": func(s *api.EnvironmentGitOpsStatusResponse) { s.WorkloadEvidence.Candidates = 2 },
		"missing graph":           func(s *api.EnvironmentGitOpsStatusResponse) { s.WorkloadEvidence = nil },
		"older server":            func(s *api.EnvironmentGitOpsStatusResponse) { s.WorkloadEvidence.CommitSHA = "" },
		"source":                  func(s *api.EnvironmentGitOpsStatusResponse) { s.WorkloadEvidence.SourceID = "foreign" },
		"environment":             func(s *api.EnvironmentGitOpsStatusResponse) { s.WorkloadEvidence.EnvironmentID = "foreign" },
		"revision":                func(s *api.EnvironmentGitOpsStatusResponse) { s.WorkloadEvidence.RevisionID = "old" },
		"generation":              func(s *api.EnvironmentGitOpsStatusResponse) { s.WorkloadEvidence.Generation-- },
		"intent":                  func(s *api.EnvironmentGitOpsStatusResponse) { s.WorkloadEvidence.IntentVersion-- },
		"graph identity":          func(s *api.EnvironmentGitOpsStatusResponse) { s.WorkloadEvidence.GraphID = "" },
		"digest":                  func(s *api.EnvironmentGitOpsStatusResponse) { s.WorkloadEvidence.DefinitionDigest = "" },
		"plan":                    func(s *api.EnvironmentGitOpsStatusResponse) { s.WorkloadEvidence.PlanHash = "" },
		"preparation":             func(s *api.EnvironmentGitOpsStatusResponse) { s.WorkloadEvidence.ArtifactsPrepared = false },
		"qualification":           func(s *api.EnvironmentGitOpsStatusResponse) { s.WorkloadEvidence.Qualified = false },
		"activation":              func(s *api.EnvironmentGitOpsStatusResponse) { s.WorkloadEvidence.Activated = false },
		"serving":                 func(s *api.EnvironmentGitOpsStatusResponse) { s.WorkloadEvidence.Serving = false },
		"empty members":           func(s *api.EnvironmentGitOpsStatusResponse) { s.WorkloadEvidence.Workloads = nil },
		"duplicate members": func(s *api.EnvironmentGitOpsStatusResponse) {
			s.WorkloadEvidence.Workloads = append(s.WorkloadEvidence.Workloads, s.WorkloadEvidence.Workloads[0])
		},
		"invalid member": func(s *api.EnvironmentGitOpsStatusResponse) { s.WorkloadEvidence.Workloads[0].AppID = "" },
		"unknown mode":   func(s *api.EnvironmentGitOpsStatusResponse) { s.WorkloadEvidence.Workloads[0].ExecutionMode = "future" },
		"member unknown": func(s *api.EnvironmentGitOpsStatusResponse) { s.WorkloadEvidence.Workloads[0].Serving = "unknown" },
		"blocker": func(s *api.EnvironmentGitOpsStatusResponse) {
			s.WorkloadEvidence.BlockingReasons = []string{"environment_serving_evidence_missing"}
		},
		"member blocker": func(s *api.EnvironmentGitOpsStatusResponse) {
			s.WorkloadEvidence.Workloads[0].BlockingReasons = []string{"environment_serving_evidence_missing"}
		},
		"scheduled occurrence": func(s *api.EnvironmentGitOpsStatusResponse) {
			s.WorkloadEvidence.Workloads[0].ExecutionMode = "job"
			s.WorkloadEvidence.Workloads[0].Serving = "unknown"
		},
		"source outage":        func(s *api.EnvironmentGitOpsStatusResponse) { s.Source.SourceErrorCode = "PRIVATE_PROVIDER_ERROR" },
		"missing verification": func(s *api.EnvironmentGitOpsStatusResponse) { s.Source.SourceVerifiedAt = nil },
		"stale verification": func(s *api.EnvironmentGitOpsStatusResponse) {
			at := now.Add(-api.EnvironmentGitSourceStaleAfter)
			s.Source.SourceVerifiedAt = &at
		},
		"future verification": func(s *api.EnvironmentGitOpsStatusResponse) {
			at := now.Add(time.Second)
			s.Source.SourceVerifiedAt = &at
		},
		"report mode": func(s *api.EnvironmentGitOpsStatusResponse) { s.Source.Spec.Mode = "report" },
		"suspended":   func(s *api.EnvironmentGitOpsStatusResponse) { s.Source.Suspended = true },
		"detached":    func(s *api.EnvironmentGitOpsStatusResponse) { s.Source.Detached = true },
	} {
		t.Run(name, func(t *testing.T) {
			status := servingStatus(now)
			mutate(&status)
			status.Explanation = &api.EnvironmentGitOpsExplanation{ReadOnly: true, Status: "serving", ServingRevisionID: "revision"}
			out, err := newTracker(t).Observe(status, now)
			if err != nil || out.Status == "serving" || out.ExitCode() == 0 {
				t.Fatal("missing evidence became success", out, err)
			}
			encoded, _ := json.Marshal(out)
			if strings.Contains(string(encoded), "PRIVATE") {
				t.Fatal("upstream error disclosed", string(encoded))
			}
		})
	}
}

func TestWaitSupersessionRequiresObservedTargetAndBindsCurrentSource(t *testing.T) {
	now := time.Now().UTC()
	for _, mode := range []string{"source", "repository", "ref", "revision", "generation", "same commit reapproved"} {
		t.Run(mode, func(t *testing.T) {
			tracker := newTracker(t)
			status := servingStatus(now)
			status.WorkloadEvidence.Serving = false
			if _, err := tracker.Observe(status, now); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "source":
				status.Source.ID = "new-source"
			case "repository":
				status.Source.Spec.RepositoryID++
			case "ref":
				status.Source.Spec.Ref = "refs/heads/other"
			case "revision":
				status.Source.ApprovedRevisionID = "other-revision"
			case "generation":
				status.Source.Generation++
			case "same commit reapproved":
				status.Source.ApprovedRevisionID = "new-approval"
				status.Source.Generation++
			}
			out, err := tracker.Observe(status, now)
			if err != nil || out.Status != "superseded" || out.ExitCode() != 4 {
				t.Fatal(out, err)
			}
		})
	}
	tracker := newTracker(t)
	status := servingStatus(now)
	status.WorkloadEvidence.Serving = false
	_, _ = tracker.Observe(status, now)
	status.Source.IntentVersion++
	if out, err := tracker.Observe(status, now); err != nil || out.Status != "waiting" {
		t.Fatal(out, err)
	}
	status.WorkloadEvidence.IntentVersion++
	status.WorkloadEvidence.GraphID = "fresh-graph"
	status.WorkloadEvidence.Serving = true
	if out, err := tracker.Observe(status, now); err != nil || out.Status != "serving" {
		t.Fatal(out, err)
	}
}

func TestWaitFailuresAreCurrentScopedAndRedacted(t *testing.T) {
	now := time.Now().UTC()
	for _, mode := range []string{"graph", "build", "qualification", "activation", "serving"} {
		t.Run(mode, func(t *testing.T) {
			status := servingStatus(now)
			switch mode {
			case "graph":
				status.WorkloadEvidence.GraphErrorCode = "PRIVATE_GRAPH_LOG"
			case "build":
				status.WorkloadEvidence.Workloads[0].BuildStatus = "failed"
			case "qualification":
				status.WorkloadEvidence.Workloads[0].Qualification = "failed"
			case "activation":
				status.WorkloadEvidence.Workloads[0].Activation = "failed"
			case "serving":
				status.WorkloadEvidence.Workloads[0].Serving = "failed"
			}
			out, err := newTracker(t).Observe(status, now)
			encoded, _ := json.Marshal(out)
			if err != nil || out.Status != "failed" || out.ExitCode() != 2 || strings.Contains(string(encoded), "PRIVATE") {
				t.Fatal(out, err, string(encoded))
			}
			status.WorkloadEvidence.CommitSHA = strings.Repeat("b", 40)
			if out, err := newTracker(t).Observe(status, now); err != nil || out.Status != "waiting" {
				t.Fatal("old failure stopped requested rollout", out, err)
			}
		})
	}
}

func TestWaitValidatesScopeAndImmutableDefinition(t *testing.T) {
	if _, err := NewTracker("main", "production"); err == nil {
		t.Fatal("mutable commit accepted")
	}
	tracker := newTracker(t)
	now := time.Now().UTC()
	status := servingStatus(now)
	status.Source.EnvironmentSlug = "staging"
	if _, err := tracker.Observe(status, now); err == nil {
		t.Fatal("foreign environment accepted")
	}
	status = servingStatus(now)
	_, _ = tracker.Observe(status, now)
	status.WorkloadEvidence.DefinitionDigest = strings.Repeat("e", 64)
	if _, err := tracker.Observe(status, now); err == nil {
		t.Fatal("revision content identity changed")
	}
}
