package tasktoolrehearsal

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

type smokeDriverFixture struct {
	initial, recovered WorkerClaim
	stopped            StoppedWorker
	result             SmokeResult
	observed           ObservedResult
	cancel             context.CancelFunc
	cancelAfterClaim   bool
	stopCalls          int
}

func (*smokeDriverFixture) CheckHost(context.Context, string) error { return nil }

func (d *smokeDriverFixture) SubmitReport(context.Context, SmokeScope) (string, error) {
	return d.initial.TaskID, nil
}

func (d *smokeDriverFixture) WaitForClaim(_ context.Context, _ SmokeScope, _ string, attempt int) (WorkerClaim, error) {
	if attempt == 1 {
		if d.cancelAfterClaim {
			d.cancel()
		}
		return d.initial, nil
	}
	return d.recovered, nil
}

func (d *smokeDriverFixture) StopWorker(context.Context, SmokeScope, WorkerClaim) (StoppedWorker, error) {
	d.stopCalls++
	return d.stopped, nil
}

func (d *smokeDriverFixture) WaitForResult(context.Context, SmokeScope, string) (SmokeResult, error) {
	return d.result, nil
}

func (d *smokeDriverFixture) ObserveResult(context.Context, SmokeScope, string) (ObservedResult, error) {
	return d.observed, nil
}

func (*smokeDriverFixture) VerifyCleanup(context.Context, SmokeScope) error { return nil }

func TestRunSmokeMatchesTheClaimAndDurableResult(t *testing.T) {
	for _, test := range []struct {
		name      string
		change    func(*smokeDriverFixture)
		wantStops int
	}{
		{"same Task and one durable result", nil, 1},
		{"foreign worker deployment", func(d *smokeDriverFixture) { d.initial.DeploymentID = uuid.NewString() }, 0},
		{"foreign queue namespace", func(d *smokeDriverFixture) { d.initial.Namespace = "other-rehearsal" }, 0},
		{"first claim cancelled before interrupt", func(d *smokeDriverFixture) { d.cancelAfterClaim = true }, 0},
		{"wrong stop receipt", func(d *smokeDriverFixture) { d.stopped.InstanceID = uuid.NewString() }, 1},
		{"same worker reclaimed", func(d *smokeDriverFixture) { d.recovered.InstanceID = d.initial.InstanceID }, 1},
		{"foreign Task recovered", func(d *smokeDriverFixture) { d.recovered.TaskID = uuid.NewString() }, 1},
		{"unexpected MCP result", func(d *smokeDriverFixture) { d.result.Text = "different report" }, 1},
		{"duplicate stored results", func(d *smokeDriverFixture) { d.observed.CompletedResultRows = 2 }, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			run, task, worker := uuid.NewString(), uuid.NewString(), uuid.NewString()
			scope := SmokeScope{RunID: run, Namespace: "rehearsal-" + run, WorkerDeploymentID: worker}
			initial := WorkerClaim{
				TaskID: task, Namespace: scope.Namespace, DeploymentID: worker,
				InstanceID: uuid.NewString(), Status: "running", Attempt: 1,
			}
			recovered := initial
			recovered.Attempt, recovered.InstanceID = 2, uuid.NewString()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			driver := smokeDriverFixture{
				initial: initial, recovered: recovered,
				stopped:  StoppedWorker{DeploymentID: worker, InstanceID: initial.InstanceID},
				result:   SmokeResult{TaskID: task, Status: "completed", Text: smokeReportResult},
				observed: ObservedResult{TaskID: task, Namespace: scope.Namespace, Attempt: 2, TaskRows: 1, CompletedResultRows: 1},
				cancel:   cancel,
			}
			if test.change != nil {
				test.change(&driver)
			}
			got, err := RunSmoke(ctx, scope, &driver)
			if driver.stopCalls != test.wantStops {
				t.Fatalf("stopped %d workers, want %d", driver.stopCalls, test.wantStops)
			}
			if test.change != nil {
				if err == nil {
					t.Fatal("unexpectedly accepted invalid or cancelled evidence")
				}
			} else if err != nil || got.TaskID != task || got.Recovered != recovered || got.Observed != driver.observed {
				t.Fatalf("valid smoke evidence was lost: %+v %v", got, err)
			}
		})
	}
}
