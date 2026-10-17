package rehearsal

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rehearsalwork"
	"testing"
)

func TestWorkerTerminationAdmissionRemainsSeparatelyGated(t *testing.T) {
	req := api.RegisterScenarioTestRequest{ManagedWork: &rehearsalwork.Plan{WorkerTermination: &rehearsalwork.WorkerTermination{Workload: "worker", Checkpoint: "after-commit"}}}
	if ValidateWorkerTermination(req, false) == nil {
		t.Fatal("unqualified worker termination admitted")
	}
	if p := ValidateWorkerTermination(req, true); p != nil {
		t.Fatal(p)
	}
	req.ManagedWork.WorkerTermination = nil
	if p := ValidateWorkerTermination(req, false); p != nil {
		t.Fatal("gate changed ordinary managed work")
	}
}
