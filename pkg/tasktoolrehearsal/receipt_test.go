package tasktoolrehearsal

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestHostedTaskRecoveryReceipt(t *testing.T) {
	run, task, deployment := uuid.NewString(), uuid.NewString(), uuid.NewString()
	initial := WorkerClaim{TaskID: task, Namespace: "rehearsal-" + run, DeploymentID: deployment, InstanceID: uuid.NewString(), Status: "running", Attempt: 1}
	recovered := initial
	recovered.InstanceID, recovered.Attempt = uuid.NewString(), 2
	evidence := SmokeEvidence{TaskID: task, Initial: initial, Stopped: StoppedWorker{DeploymentID: deployment, InstanceID: initial.InstanceID},
		Recovered: recovered, Result: SmokeResult{TaskID: task, Status: "completed", Text: smokeReportResult},
		Observed: ObservedResult{TaskID: task, Namespace: initial.Namespace, Attempt: 2, TaskRows: 1, CompletedResultRows: 1}, CleanupConfirmed: true}
	archives := FixtureArchives{Web: strings.Repeat("a", 64), Worker: strings.Repeat("b", 64)}
	for _, tc := range []struct {
		name   string
		mutate func(*SmokeEvidence, *CleanupObservation)
	}{
		{"valid", nil},
		{"same worker", func(e *SmokeEvidence, _ *CleanupObservation) { e.Recovered.InstanceID = e.Initial.InstanceID }},
		{"claim for another Task", func(e *SmokeEvidence, _ *CleanupObservation) { e.Recovered.TaskID = uuid.NewString() }},
		{"claim from another namespace", func(e *SmokeEvidence, _ *CleanupObservation) { e.Initial.Namespace += "-other" }},
		{"wrong stopped worker", func(e *SmokeEvidence, _ *CleanupObservation) { e.Stopped.InstanceID = uuid.NewString() }},
		{"duplicate result", func(e *SmokeEvidence, _ *CleanupObservation) { e.Observed.CompletedResultRows = 2 }},
		{"cleanup not confirmed", func(e *SmokeEvidence, _ *CleanupObservation) { e.CleanupConfirmed = false }},
		{"source remains", func(_ *SmokeEvidence, c *CleanupObservation) { c.SourcesRemaining = 1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, cleanup := evidence, CleanupObservation{}
			if tc.mutate != nil {
				tc.mutate(&e, &cleanup)
			}
			got, err := NewRecoveryReceipt(run, archives, e, cleanup)
			if tc.mutate != nil {
				if err == nil || got != (RecoveryReceipt{}) {
					t.Fatalf("invalid evidence emitted a receipt: %+v, %v", got, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), `"completed_result_rows":1`) || !strings.Contains(string(data), `"stopped_instance_id":"`+initial.InstanceID+`"`) {
				t.Fatalf("unexpected receipt: %s", data)
			}
		})
	}
}
