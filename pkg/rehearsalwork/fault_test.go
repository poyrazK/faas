package rehearsalwork

import "testing"

func TestWorkerTerminationRequiresOriginalReceiptAndFreshRecovery(t *testing.T) {
	for _, mode := range []string{"complete", "missing_receipt", "uncertain_cleanup", "same_vm", "wrong_job", "no_retry", "extra_job", "unknown_attempt", "old_vm_later"} {
		t.Run(mode, func(t *testing.T) {
			e := validEvidence()
			e.Plan.WorkerTermination = &WorkerTermination{Workload: "worker", Checkpoint: "after-commit"}
			_, e.PlanSHA256, _ = Normalize(e.Plan, []string{"primary", "worker"})
			j := &e.Jobs[0]
			j.Attempts[0].Outcome = "retry"
			a := j.Attempts[0]
			a.Number, a.Outcome, a.InstanceID = 2, "succeeded", "77777777-7777-4777-8777-777777777777"
			j.Attempts = append(j.Attempts, a)
			e.WorkerFault = &WorkerFault{ID: "88888888-8888-4888-8888-888888888888", AccountID: e.Bindings[0].AppID, AppID: j.AppID, RunID: e.RunID, Workload: j.Workload, DeploymentID: j.DeploymentID, InstanceID: j.Attempts[0].InstanceID, InvocationID: j.ID, CheckpointAttemptID: "99999999-9999-4999-8999-999999999999", RecorderSessionID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", State: "terminated"}
			switch mode {
			case "missing_receipt":
				e.WorkerFault = nil
			case "uncertain_cleanup":
				e.WorkerFault.State = "fenced"
			case "same_vm":
				j.Attempts[1].InstanceID = j.Attempts[0].InstanceID
			case "wrong_job":
				e.WorkerFault.InvocationID = e.WorkerFault.ID
			case "no_retry":
				j.Attempts = j.Attempts[:1]
				j.Attempts[0].Outcome = "succeeded"
			case "extra_job":
				other := *j
				other.ID = e.WorkerFault.ID
				e.Jobs = append(e.Jobs, other)
			case "unknown_attempt":
				j.Attempts[0].Outcome = "unknown"
			case "old_vm_later":
				j.Attempts[1].Outcome = "retry"
				a := j.Attempts[0]
				a.Number, a.Outcome = 3, "succeeded"
				j.Attempts = append(j.Attempts, a)
			}
			e.BindRecovery()
			if err := e.Validate(); (err == nil) != (mode == "complete") {
				t.Fatalf("%s: %v", mode, err)
			}
		})
	}
}
