package apidserver

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProjectRollbackRecoveryWaitsForAllHTTPWorkloads(t *testing.T) {
	started := time.Now()
	verified1, verified2 := started.Add(time.Second), started.Add(3*time.Second)
	op := state.ProjectRollbackRecord{ProjectRollbackOperation: api.ProjectRollbackOperation{
		Status: "complete", CreatedAt: &started, Steps: []api.ProjectRollbackStep{
			{AppID: "web", AttemptID: "web-attempt"},
			{AppID: "api", AttemptID: "api-attempt"},
			{AppID: "worker", AttemptID: "worker-attempt"},
		},
	}, HTTPWorkloads: []string{"web", "api"}}
	seen := map[string]api.ProjectRollbackTraffic{
		"web-attempt": {Status: "verified", FirstSuccessAt: &verified1},
		"api-attempt": {Status: "awaiting_traffic"},
	}
	projectRollbackTraffic(&op, seen, true)
	if op.RecoveryMS != nil || op.Steps[1].Traffic.Status != "awaiting_traffic" || op.Steps[2].Traffic != nil {
		t.Fatalf("premature verification: %+v", op.ProjectRollbackOperation)
	}
	seen["api-attempt"] = api.ProjectRollbackTraffic{Status: "verified", FirstSuccessAt: &verified2}
	projectRollbackTraffic(&op, seen, true)
	if op.RecoveryMS == nil || *op.RecoveryMS != 3000 {
		t.Fatalf("recovery = %v, want 3000ms", op.RecoveryMS)
	}

	op.Status = "blocked"
	op.Steps[1].PreparationStatus = "released"
	seen["api-attempt"] = api.ProjectRollbackTraffic{Status: "awaiting_cutover"}
	projectRollbackTraffic(&op, seen, true)
	if op.RecoveryMS != nil || op.Steps[1].Traffic != nil {
		t.Fatalf("released preparation still waiting for HTTP recovery: %+v", op.ProjectRollbackOperation)
	}
}
