// adr: 1359
package rehearsalresume

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestPlanPinsCheckpointInventoryAndProtocolBounds(t *testing.T) {
	s := Spec{Version: 1, Checkpoint: "after_setup", Workloads: []string{"worker", "primary"}, PauseMS: 1, WithinMS: 1000}
	a, d, err := Normalize(s, []string{"worker", "primary"})
	if err != nil {
		t.Fatal(err)
	}
	b, e, err := Normalize(Decode(string(a)), []string{"primary", "worker"})
	if err != nil || string(a) != string(b) || d != e {
		t.Fatal("plan is not canonical")
	}
	for _, change := range []func(*Spec){func(s *Spec) { s.Checkpoint = "in_flight" }, func(s *Spec) { s.Workloads = []string{"worker", "worker"} }, func(s *Spec) { s.Workloads = []string{"missing"} }, func(s *Spec) { s.PauseMS = 0 }, func(s *Spec) { s.PauseMS = MaxPauseMS + 1 }, func(s *Spec) { s.WithinMS = MaxWithinMS + 1 }} {
		c := s
		change(&c)
		if _, _, err := Normalize(c, []string{"primary", "worker"}); err == nil {
			t.Fatal("unsupported plan accepted")
		}
	}
}

func TestEvidenceRequiresCompleteOriginalOwnerInventory(t *testing.T) {
	s := Spec{Version: 1, Checkpoint: "after_setup", Workloads: []string{"worker"}, PauseMS: 10, WithinMS: 1000}
	_, d, _ := Normalize(s, []string{"primary", "worker"})
	valid := func() Evidence {
		return Evidence{Version: 1, RunID: strings.Repeat("a", 32), Spec: s, PlanSHA256: d, Status: "complete", Receipts: []Receipt{{ID: uuid.NewString(), AccountID: uuid.NewString(), AppID: uuid.NewString(), RunID: strings.Repeat("a", 32), Workload: "worker", DeploymentID: uuid.NewString(), InstanceID: uuid.NewString(), RecorderSessionID: uuid.NewString(), State: "complete", Method: "in_place", PauseMS: 10, ResumeMS: 100}}}
	}
	e := valid()
	if err := e.Validate([]string{"primary", "worker"}); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Evidence){func(e *Evidence) { e.Receipts = nil }, func(e *Evidence) { e.Receipts[0].State = "resumed" }, func(e *Evidence) { e.Receipts[0].RecorderSessionID = "" }, func(e *Evidence) { e.Receipts[0].Method = "restore" }, func(e *Evidence) { e.Receipts[0].PauseMS = 9 }, func(e *Evidence) { e.Receipts[0].ResumeMS = -1 }, func(e *Evidence) { r := &e.Receipts[0]; r.InstanceID = strings.ReplaceAll(r.AppID, "-", "") }} {
		e := valid()
		change(&e)
		if e.Validate([]string{"primary", "worker"}) == nil {
			t.Fatal("invalid proof passed")
		}
	}
}

func TestRunningSuspendRequiresFirstDispatchAndCheckpointIdentities(t *testing.T) {
	spec := Spec{Version: 1, Checkpoint: "managed_job", Workloads: []string{"worker"}, WorkerCheckpoint: "after-commit", PauseMS: 1, WithinMS: 1000}
	_, digest, err := Normalize(spec, []string{"primary", "worker"})
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"valid", "missing_job", "missing_checkpoint", "second_attempt", "multiple_workers", "unnamed_checkpoint"} {
		t.Run(mode, func(t *testing.T) {
			e := Evidence{Version: 1, RunID: strings.Repeat("a", 32), Spec: spec, PlanSHA256: digest, Status: "complete", Receipts: []Receipt{{ID: uuid.NewString(), AccountID: uuid.NewString(), AppID: uuid.NewString(), RunID: strings.Repeat("a", 32), Workload: "worker", DeploymentID: uuid.NewString(), InstanceID: uuid.NewString(), RecorderSessionID: uuid.NewString(), State: "complete", Method: "in_place", PauseMS: 1, ResumeMS: 1, InvocationID: uuid.NewString(), InvocationAttempt: 1, CheckpointAttemptID: uuid.NewString()}}}
			switch mode {
			case "missing_job":
				e.Receipts[0].InvocationID = ""
			case "missing_checkpoint":
				e.Receipts[0].CheckpointAttemptID = ""
			case "second_attempt":
				e.Receipts[0].InvocationAttempt = 2
			case "multiple_workers":
				e.Spec.Workloads = []string{"primary", "worker"}
			case "unnamed_checkpoint":
				e.Spec.WorkerCheckpoint = ""
			}
			if (e.Validate([]string{"primary", "worker"}) == nil) != (mode == "valid") {
				t.Fatal("running checkpoint evidence did not enforce its supported scope")
			}
		})
	}
}
