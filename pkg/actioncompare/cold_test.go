package actioncompare

import (
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rehearsalcold"
	"strings"
	"testing"
)

func coldActionEvidence() *Evidence {
	e := actionEvidence()
	s := e.Snapshot
	e.Provenance = SimulationProvenance
	s.Version, s.Scope, s.SimulationVersion, s.SimulationSHA256 = 2, api.ScenarioSimulationScope, 1, strings.Repeat("d", 64)
	s.RecorderVersion, s.RecorderSessions = 1, 2
	spec := rehearsalcold.Spec{Version: 1, WithinMS: 500}
	_, digest, _ := rehearsalcold.Normalize(spec, s.Workloads)
	s.ColdStart = &rehearsalcold.Evidence{Version: 1, RunID: s.RunID, Spec: spec, PlanSHA256: digest, Status: "complete", Preparations: []rehearsalcold.Preparation{{ID: uuid.NewString(), AccountID: uuid.NewString(), AppID: uuid.NewString(), DeploymentID: uuid.NewString(), RunID: s.RunID, Workload: s.Workloads[0], InstanceID: uuid.NewString(), RecorderSessionID: uuid.NewString(), State: "booted", BootInstanceID: uuid.NewString(), BootRecorderSessionID: uuid.NewString(), WakeID: uuid.NewString(), Method: "cold_boot", StartupMS: 100}}}
	return e
}

func TestColdStartComparisonSeparatesBudgetFailureFromMissingProof(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		change     func(*Evidence)
	}{
		{"fresh_identities_and_timing_jitter", "passed", func(e *Evidence) { e.Snapshot.ColdStart.Preparations[0].StartupMS = 200 }},
		{"startup_budget_exceeded", "failed", func(e *Evidence) { e.Snapshot.ColdStart.Preparations[0].StartupMS = 501 }},
		{"no_original_retirement", "inconclusive", func(e *Evidence) { e.Snapshot.ColdStart.Preparations[0].RecorderSessionID = "" }},
		{"no_boot_receipt", "inconclusive", func(e *Evidence) { e.Snapshot.ColdStart.Preparations[0].BootRecorderSessionID = "" }},
		{"reused_old_vm", "inconclusive", func(e *Evidence) { p := &e.Snapshot.ColdStart.Preparations[0]; p.BootInstanceID = p.InstanceID }},
		{"old_vm_uuid_alias", "inconclusive", func(e *Evidence) {
			p := &e.Snapshot.ColdStart.Preparations[0]
			p.InstanceID = strings.ReplaceAll(p.BootInstanceID, "-", "")
		}},
		{"restored_vm", "inconclusive", func(e *Evidence) { e.Snapshot.ColdStart.Preparations[0].Method = "restore" }},
		{"missing_participant", "inconclusive", func(e *Evidence) { e.Snapshot.ColdStart.Preparations = nil }},
		{"warm_coverage", "inconclusive", func(e *Evidence) { e.Snapshot.ColdStart = nil }},
		{"changed_budget", "inconclusive", func(e *Evidence) {
			c := e.Snapshot.ColdStart
			c.Spec.WithinMS = 600
			_, c.PlanSHA256, _ = rehearsalcold.Normalize(c.Spec, e.Snapshot.Workloads)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, b := coldActionEvidence(), coldActionEvidence()
			tc.change(b)
			r := Compare(a, b)
			if r.Status != tc.want {
				t.Fatalf("%s: %+v", tc.want, r)
			}
		})
	}
	a, b := coldActionEvidence(), coldActionEvidence()
	a.Snapshot.ColdStart.Preparations[0].StartupMS = 1000
	b.Snapshot.ColdStart.Preparations[0].StartupMS = 1001
	if r := Compare(a, b); r.Status != "failed" {
		t.Fatalf("two budget violations passed: %+v", r)
	}
}
