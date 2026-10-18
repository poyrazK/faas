package actioncompare

import (
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/rehearsalresume"
)

func suspendActionEvidence() *Evidence {
	e := coldActionEvidence()
	s := e.Snapshot
	s.ColdStart = nil
	spec := rehearsalresume.Spec{Version: 1, Checkpoint: "after_setup", Workloads: s.Workloads, PauseMS: 10, WithinMS: 500}
	_, d, _ := rehearsalresume.Normalize(spec, s.Workloads)
	s.SuspendResume = &rehearsalresume.Evidence{Version: 1, RunID: s.RunID, Spec: spec, PlanSHA256: d, Status: "complete", Receipts: []rehearsalresume.Receipt{{ID: uuid.NewString(), AccountID: uuid.NewString(), AppID: uuid.NewString(), RunID: s.RunID, Workload: s.Workloads[0], DeploymentID: uuid.NewString(), InstanceID: uuid.NewString(), RecorderSessionID: uuid.NewString(), State: "complete", Method: "in_place", PauseMS: 10, ResumeMS: 100}}}
	return e
}
func TestSuspendResumeComparisonSeparatesBudgetAndMissingProof(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		change     func(*Evidence)
	}{
		{"new_identities_and_timing_jitter", "passed", func(e *Evidence) { e.Snapshot.SuspendResume.Receipts[0].ResumeMS = 200 }},
		{"readiness_budget", "failed", func(e *Evidence) { e.Snapshot.SuspendResume.Receipts[0].ResumeMS = 501 }},
		{"missing_owner", "inconclusive", func(e *Evidence) { e.Snapshot.SuspendResume.Receipts[0].RecorderSessionID = "" }},
		{"not_published", "inconclusive", func(e *Evidence) { e.Snapshot.SuspendResume.Receipts[0].State = "resumed" }},
		{"restored_replacement", "inconclusive", func(e *Evidence) { e.Snapshot.SuspendResume.Receipts[0].Method = "restore" }},
		{"missing_coverage", "inconclusive", func(e *Evidence) { e.Snapshot.SuspendResume = nil }},
		{"different_plan", "inconclusive", func(e *Evidence) {
			p := e.Snapshot.SuspendResume
			p.Spec.PauseMS = 11
			p.Receipts[0].PauseMS = 11
			_, p.PlanSHA256, _ = rehearsalresume.Normalize(p.Spec, e.Snapshot.Workloads)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, b := suspendActionEvidence(), suspendActionEvidence()
			tc.change(b)
			if r := Compare(a, b); r.Status != tc.want {
				t.Fatalf("want %s: %+v", tc.want, r)
			}
		})
	}
	a, b := suspendActionEvidence(), suspendActionEvidence()
	a.Snapshot.SuspendResume.Receipts[0].ResumeMS = 501
	b.Snapshot.SuspendResume.Receipts[0].ResumeMS = 502
	if r := Compare(a, b); r.Status != "failed" {
		t.Fatalf("two budget violations passed: %+v", r)
	}
}
