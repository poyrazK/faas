// adr: 20261018000100
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/actioncompare"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/behaviorcompare"
	"github.com/onebox-faas/faas/pkg/rehearsalobj"
	"github.com/onebox-faas/faas/pkg/rehearsalpg"
	"github.com/onebox-faas/faas/pkg/rehearsalresume"
	"github.com/onebox-faas/faas/pkg/rehearsalwork"
)

const account = "11111111-1111-4111-8111-111111111111"

func validateRun(r run, account string, notifications int) error {
	return validateRunJourney(r, account, notifications, "running-job")
}

func verify(control, regression report, baseline, candidate, account string) error {
	return verifyJourney(control, regression, baseline, candidate, account, "running-job")
}

func fixtureRun(t *testing.T, notifications int) run {
	t.Helper()
	r := run{Scenario: "running-import", Engine: "real-vm", Profile: "warm", Status: "passed", Attempt: 1,
		RunID: strings.ReplaceAll(uuid.NewString(), "-", ""), AppSlug: "api-" + uuid.NewString(), SourceSHA256: strings.Repeat("7", 64),
		Services:              map[string]string{"worker": "worker-" + uuid.NewString()},
		WorkloadAppIDs:        map[string]string{"import-api": uuid.NewString(), "worker": uuid.NewString()},
		WorkloadDeploymentIDs: map[string]string{"import-api": uuid.NewString(), "worker": uuid.NewString()}}
	r.DeploymentID = r.WorkloadDeploymentIDs["import-api"]
	pgSpec := rehearsalpg.Spec{Version: 1, FixtureSHA256: strings.Repeat("a", 64), MaxLockWaitMS: 250, Tables: []rehearsalpg.Table{{Schema: "public", Name: "imports", Columns: []string{"external_id", "state"}}}}
	before := []rehearsalpg.Snapshot{{Table: "public.imports", Rows: []json.RawMessage{[]byte(`{"external_id":"import-001","state":"pending"}`)}}}
	after := []rehearsalpg.Snapshot{{Table: "public.imports", Rows: []json.RawMessage{[]byte(`{"external_id":"import-001","state":"complete"}`)}}}
	r.Postgres = &rehearsalpg.Evidence{Receipt: rehearsalpg.Receipt{Version: 1, RunID: r.RunID, AppID: r.WorkloadAppIDs["import-api"], ResourceID: uuid.NewString(), FixtureSHA256: pgSpec.FixtureSHA256, ContractSHA256: pgSpec.ContractSHA256(), InitialSHA256: rehearsalpg.Hash(before)},
		Status: "complete", Provenance: rehearsalpg.Provenance, DeploymentID: r.DeploymentID, Spec: pgSpec, Before: before, After: after, Changes: rehearsalpg.Changes(before, after), LockSamples: 10, LockBudgetPassed: true, CleanupConfirmed: true}
	objects, err := rehearsalobj.Snapshot([]rehearsalobj.Data{{Key: "exports/import-001.csv", Body: []byte(csv), ContentType: "text/csv", Metadata: map[string]string{}}})
	if err != nil {
		t.Fatal(err)
	}
	initial := []rehearsalobj.Object{}
	objSpec := rehearsalobj.Spec{Version: 1, Bucket: "imports", FixtureSHA256: rehearsalobj.Hash(initial)}
	r.Objects = &rehearsalobj.Evidence{Receipt: rehearsalobj.Receipt{Version: 1, RunID: r.RunID, AppID: r.WorkloadAppIDs["import-api"], ResourceID: uuid.NewString(), CollectorID: uuid.NewString(), FixtureSHA256: objSpec.FixtureSHA256, ContractSHA256: objSpec.ContractSHA256(), InitialSHA256: objSpec.FixtureSHA256},
		Status: "complete", Provenance: rehearsalobj.Provenance, DeploymentID: r.DeploymentID, Spec: objSpec, Before: initial, After: objects,
		Mutations: []rehearsalobj.Mutation{{Workload: "worker", Sequence: 1, Method: "PUT", Key: objects[0].Key, Status: 200, Object: &objects[0]}}, CleanupConfirmed: true, CollectorClosed: true}
	spec := rehearsalresume.Spec{Version: 1, Checkpoint: "managed_job", Workloads: []string{"worker"}, WorkerCheckpoint: "after-commit", PauseMS: 1000, WithinMS: 5000}
	_, resumeHash, err := rehearsalresume.Normalize(spec, []string{"import-api", "worker"})
	if err != nil {
		t.Fatal(err)
	}
	p := rehearsalresume.Receipt{ID: uuid.NewString(), AccountID: account, AppID: r.WorkloadAppIDs["worker"], RunID: r.RunID, Workload: "worker", DeploymentID: r.WorkloadDeploymentIDs["worker"], InstanceID: uuid.NewString(), RecorderSessionID: uuid.NewString(), State: "complete", Method: "in_place", PauseMS: 1000, ResumeMS: 50, InvocationID: uuid.NewString(), InvocationAttempt: 1, CheckpointAttemptID: uuid.NewString()}
	r.SuspendResume = &rehearsalresume.Evidence{Version: 1, RunID: r.RunID, Spec: spec, PlanSHA256: resumeHash, Status: "complete", Receipts: []rehearsalresume.Receipt{p}}
	plan := rehearsalwork.Plan{Version: 1, Primary: "import-api", Participants: []rehearsalwork.Participant{{Workload: "import-api", Postgres: "read_write", Objects: "read_write"}, {Workload: "worker", Async: true, Postgres: "read_write", Objects: "read_write"}}}
	_, workHash, err := rehearsalwork.Normalize(plan, []string{"import-api", "worker"})
	if err != nil {
		t.Fatal(err)
	}
	work := &rehearsalwork.Evidence{Version: 1, RunID: r.RunID, PlanSHA256: workHash, Status: "complete", Closing: true, Plan: plan}
	for _, name := range []string{"import-api", "worker"} {
		work.Bindings = append(work.Bindings, rehearsalwork.Binding{Workload: name, AppID: r.WorkloadAppIDs[name], DeploymentID: r.WorkloadDeploymentIDs[name], PostgresResourceID: r.Postgres.ResourceID, ObjectsResourceID: r.Objects.ResourceID})
	}
	work.Jobs = []rehearsalwork.Job{{ID: p.InvocationID, Workload: "worker", AppID: p.AppID, DeploymentID: p.DeploymentID, Method: "POST", Path: "/import", PayloadSHA256: rehearsalwork.PayloadHash([]byte(`{"external_id":"import-001"}`)), State: "completed", Attempts: []rehearsalwork.Attempt{{Number: 1, InstanceID: p.InstanceID, DeploymentID: p.DeploymentID, Outcome: "retry"}, {Number: 2, InstanceID: p.InstanceID, DeploymentID: p.DeploymentID, Outcome: "succeeded"}}}}
	s := &api.ScenarioActionSnapshot{Version: 2, Scope: api.ScenarioSimulationScope, SimulationVersion: 1, SimulationSHA256: strings.Repeat("c", 64), RecorderVersion: 1, RecorderSessions: 5, Workloads: []string{"import-api", "worker"}, RunID: r.RunID, Status: "complete", SealedAt: "2026-10-18T00:00:00Z", Intents: []api.ScenarioCapturedAction{}, SuspendResume: r.SuspendResume, ManagedWork: work}
	s.SimulatedAttempts = []api.ScenarioSimulationAttempt{{ID: p.CheckpointAttemptID, Workload: "worker", Fixture: "after-commit", Integration: "checkpoint", Adapter: "http", Method: "POST", Path: "/after-commit", RequestSHA256: rehearsalwork.Hash([]byte(`{}`)), Payload: []byte(`{}`), Attempt: 1, Status: "completed", ResponseStatus: 200}}
	for i := 1; i <= notifications; i++ {
		s.SimulatedAttempts = append(s.SimulatedAttempts, api.ScenarioSimulationAttempt{ID: uuid.NewString(), Workload: "worker", Fixture: "notification", Integration: "email", Adapter: "resend", Method: "POST", Path: "/emails", RequestSHA256: rehearsalwork.PayloadHash([]byte(notificationBody)), IdempotencySHA256: rehearsalwork.Hash([]byte("import-001:notification")), Payload: []byte(`{"subject":"Import complete","to":["customer@example.test"]}`), Attempt: i, Status: "completed", ResponseStatus: 200})
	}
	r.Behavior = &behaviorcompare.Evidence{Version: 1, Status: "complete", Provenance: behaviorcompare.Provenance, ContractSHA256: strings.Repeat("d", 64), FixtureSHA256: strings.Repeat("f", 64), Specs: []behaviorcompare.Spec{{Key: "api.ready", Kind: "http"}}, Observations: []behaviorcompare.Observation{{Key: "api.ready", Value: []byte(`{"ready":true}`)}}}
	actions, err := json.Marshal(map[string]any{"status": "applied", "policy_version": 1, "network": "contained", "webhooks": "capture", "simulation_sha256": s.SimulationSHA256, "managed_work_sha256": workHash, "suspend_resume_sha256": resumeHash,
		"evidence": &actioncompare.Evidence{Version: 1, Status: "complete", Provenance: actioncompare.SimulationProvenance, ContractSHA256: r.Behavior.ContractSHA256, FixtureSHA256: r.Behavior.FixtureSHA256, Snapshot: s}})
	if err != nil || json.Unmarshal(actions, &r.Actions) != nil {
		t.Fatal("encode action receipt")
	}
	invocations, _ := json.Marshal([]map[string]any{{"id": p.InvocationID, "service": "worker", "state": "completed", "attempts": 2}})
	if err = json.Unmarshal(invocations, &r.Invocations); err != nil {
		t.Fatal(err)
	}
	zero := 0
	for _, stage := range []string{"setup", "trigger", "assertion"} {
		r.Commands = append(r.Commands, commandReceipt{Stage: stage, TaskID: uuid.NewString(), DeploymentID: r.DeploymentID, SpecSHA256: strings.Repeat("8", 64), ReceiptSHA256: strings.Repeat("9", 64), Status: "succeeded", ExitCode: &zero})
	}
	return r
}

func fixtureReports(t *testing.T) (report, report, string, string) {
	t.Helper()
	base := revision{Commit: strings.Repeat("a", 40), Tree: strings.Repeat("b", 40), ArchiveSHA256: strings.Repeat("c", 64), SourceSHA256: strings.Repeat("d", 64)}
	candidate := revision{Commit: strings.Repeat("e", 40), Tree: strings.Repeat("f", 40), ArchiveSHA256: strings.Repeat("1", 64), SourceSHA256: strings.Repeat("2", 64)}
	inputs, _ := json.Marshal([]map[string]string{{"path": "commands.mjs", "sha256": strings.Repeat("f", 64)}, {"path": "fixture.sql", "sha256": strings.Repeat("a", 64)}, {"path": "input.json", "sha256": strings.Repeat("e", 64)}})
	control := report{Version: 1, ID: uuid.NewString(), Status: "passed", Scenario: "running-import", ManifestSHA256: strings.Repeat("3", 64), RecipeSHA256: strings.Repeat("4", 64), TrustedFiles: inputs, Baseline: base, Candidate: base, BaselineRuns: []run{fixtureRun(t, 1)}, CandidateRuns: []run{fixtureRun(t, 1)}}
	control.Comparison.ActionStatus, control.Comparison.BehaviorStatus, control.Comparison.PostgresStatus, control.Comparison.ObjectStatus = "passed", "passed", "passed", "passed"
	regression := control
	regression.ID, regression.Status, regression.Candidate = uuid.NewString(), "failed", candidate
	regression.BaselineRuns, regression.CandidateRuns = []run{fixtureRun(t, 1)}, []run{fixtureRun(t, 2)}
	regression.Comparison.ActionStatus = "failed"
	return control, regression, base.Commit, candidate.Commit
}

func TestQualificationRequiresControlAndSpecificRegression(t *testing.T) {
	control, regression, baseline, candidate := fixtureReports(t)
	if err := verify(control, regression, baseline, candidate, account); err != nil {
		t.Fatal(err)
	}
	// Exercise the actual JSON round trip, including the two independently read lifecycle proofs.
	for _, r := range []report{control, regression} {
		body, _ := json.Marshal(r)
		path := filepath.Join(t.TempDir(), "report.json")
		if err := os.WriteFile(path, body, 0600); err != nil {
			t.Fatal(err)
		}
		decoded, err := readReport(path)
		if err != nil || validateRun(decoded.CandidateRuns[0], account, len(r.CandidateRuns[0].Actions.Evidence.Snapshot.SimulatedAttempts)-1) != nil {
			t.Fatalf("round trip failed: %v", err)
		}
	}
}

func TestIncompleteOrUnrelatedEvidenceCannotQualify(t *testing.T) {
	cases := map[string]func(*report){
		"missing command collectors": func(r *report) { r.CandidateRuns[0].Actions.Evidence.Snapshot.RecorderSessions = 2 },
		"simulation":                 func(r *report) { r.CandidateRuns[0].Engine = "simulated" },
		"skip":                       func(r *report) { r.CandidateRuns[0].SkipReason = "KVM unavailable" },
		"cleanup":                    func(r *report) { r.CleanupError = "namespace retained" },
		"command failure":            func(r *report) { r.CandidateRuns[0].Commands[1].Status = "failed" },
		"command truncation":         func(r *report) { r.CandidateRuns[0].Commands[2].OutputTruncated = true },
		"no command retirement":      func(r *report) { r.CandidateRuns[0].Commands[0].ReceiptSHA256 = "" },
		"lost checkpoint": func(r *report) {
			r.CandidateRuns[0].Actions.Evidence.Snapshot.SimulatedAttempts = r.CandidateRuns[0].Actions.Evidence.Snapshot.SimulatedAttempts[1:]
		},
		"lost journal": func(r *report) { r.CandidateRuns[0].Actions.Evidence.Snapshot.ManagedWork = nil },
		"replacement retry": func(r *report) {
			r.CandidateRuns[0].Actions.Evidence.Snapshot.ManagedWork.Jobs[0].Attempts[1].InstanceID = uuid.NewString()
		},
		"incomplete recorder": func(r *report) { r.CandidateRuns[0].Actions.Evidence.Snapshot.IncompleteRecorderSessions = 1 },
		"new collector":       func(r *report) { r.CandidateRuns[0].SuspendResume.Receipts[0].RecorderSessionID = uuid.NewString() },
		"wrong account":       func(r *report) { r.CandidateRuns[0].SuspendResume.Receipts[0].AccountID = uuid.NewString() },
		"wrong dispatch": func(r *report) {
			r.CandidateRuns[0].Actions.Evidence.Snapshot.ManagedWork.Jobs[0].ID = uuid.NewString()
		},
		"no actual retry":        func(r *report) { r.CandidateRuns[0].Actions.Evidence.Snapshot.ManagedWork.Jobs[0].Attempts = nil },
		"PG retained":            func(r *report) { r.CandidateRuns[0].Postgres.CleanupConfirmed = false },
		"object retained":        func(r *report) { r.CandidateRuns[0].Objects.CleanupConfirmed = false },
		"collector not closed":   func(r *report) { r.CandidateRuns[0].Objects.CollectorClosed = false },
		"foreign object fixture": func(r *report) { r.CandidateRuns[0].Objects.RunID = uuid.NewString() },
		"foreign resource binding": func(r *report) {
			r.CandidateRuns[0].Actions.Evidence.Snapshot.ManagedWork.Bindings[1].PostgresResourceID = uuid.NewString()
		},
		"no object operation": func(r *report) { r.CandidateRuns[0].Objects.Mutations = nil },
		"lock budget exceeded": func(r *report) {
			r.CandidateRuns[0].Postgres.MaxObservedLockWaitMS = 251
			r.CandidateRuns[0].Postgres.LockBudgetPassed = false
		},
		"invented comparison": func(r *report) { r.Comparison.ActionStatus = "passed" },
		"missing duplicate": func(r *report) {
			r.CandidateRuns[0].Actions.Evidence.Snapshot.SimulatedAttempts = r.CandidateRuns[0].Actions.Evidence.Snapshot.SimulatedAttempts[:2]
		},
		"unrelated external action": func(r *report) { r.CandidateRuns[0].Actions.Evidence.Snapshot.SimulatedAttempts[1].Fixture = "other" },
		"forged provenance":         func(r *report) { r.CandidateRuns[0].Postgres.Provenance = "application_reported" },
		"changed observer":          func(r *report) { r.TrustedFiles = []byte(`[{"path":"other"}]`) },
		"missing trusted inputs":    func(r *report) { r.TrustedFiles = []byte(`[]`) },
		"reused run":                func(r *report) { r.CandidateRuns[0] = r.BaselineRuns[0] },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			control, regression, baseline, candidate := fixtureReports(t)
			mutate(&regression)
			if verify(control, regression, baseline, candidate, account) == nil {
				t.Fatal("incomplete or unrelated evidence qualified")
			}
		})
	}
	control, regression, baseline, candidate := fixtureReports(t)
	control.Status = "failed"
	if verify(control, regression, baseline, candidate, account) == nil {
		t.Fatal("a broken control was mistaken for regression detection")
	}
}

func TestRejectAmbiguousReportJSON(t *testing.T) {
	for _, body := range []string{`{"status":"failed","status":"passed"}`, `{} {}`, `null`, `[]`} {
		path := filepath.Join(t.TempDir(), "report.json")
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		_, err := readReport(path)
		if err == nil {
			t.Fatalf("ambiguous report accepted: %s", body)
		}
	}
}
