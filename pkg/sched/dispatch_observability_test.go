// Hunt 13 H13-10/H13-11: dispatch metrics separate customer limits from the
// platform failing to start work, and record how late due workflow runs are
// claimed.
package sched_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

func TestJobDispatchResultSeparatesCustomerLimitsFromPlatformFailures(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{&state.JobQuotaError{Scope: state.JobQuotaScopeConcurrent, Limit: 3, Observed: 4}, wire.JobDispatchQuota},
		{fmt.Errorf("wake: %w", financial.ErrComputeBudget), wire.JobDispatchBudget},
		{fmt.Errorf("wake: %w", financial.ErrComputeUnavailable), wire.JobDispatchComputeUnavailable},
		{errors.Join(sched.ErrPermanentWake, errors.New("account suspended")), wire.JobDispatchAccountInactive},
		{sched.ErrJobTaskAlreadyClaimed, wire.JobDispatchNotReady},
		{sched.ErrJobRunNotEligible, wire.JobDispatchNotReady},
		{fmt.Errorf("sched: WakeJob admit: %w", api.ErrCapacity("node full")), wire.JobDispatchNodeCapacity},
		{fmt.Errorf("sched: WakeJob placement: %w", state.ErrNodeCapacity), wire.JobDispatchNodeCapacity},
		{errors.New("sched: WakeJob lease: connection reset"), wire.JobDispatchWakeError},
	} {
		if got := sched.JobDispatchResult(tc.err); got != tc.want {
			t.Errorf("JobDispatchResult(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}

type okStepExecutor struct{}

func (okStepExecutor) ExecuteStep(context.Context, string, string, string, map[string]string, []byte, time.Duration) (int, []byte, error) {
	return 200, []byte(`{}`), nil
}

func dispatchLagSamples(t *testing.T, reg *prometheus.Registry) (uint64, float64) {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != "workflow_dispatch_lag_seconds" {
			continue
		}
		var count uint64
		var sum float64
		for _, metric := range family.GetMetric() {
			count += metric.GetHistogram().GetSampleCount()
			sum += metric.GetHistogram().GetSampleSum()
		}
		return count, sum
	}
	return 0, 0
}

func TestWorkflowDispatchRecordsLagOfDueRuns(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	spec := api.WorkflowSpec{Name: "late", Steps: []api.WorkflowStepSpec{{Name: "work", Run: "work"}}}
	snapshot, _ := json.Marshal(spec)
	run := &state.WorkflowRun{AppID: "app-lag", WorkflowName: spec.Name, Input: json.RawMessage(`{}`),
		DefinitionSnapshot: snapshot, ScheduledFor: time.Now().Add(-2 * time.Minute)}
	if err := store.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	reg := prometheus.NewRegistry()
	if err := sched.NewWorkflowOrchestrator(store, okStepExecutor{}, nil, wire.NewWorkflowMetrics(reg), nil).DispatchTick(ctx); err != nil {
		t.Fatal(err)
	}
	count, sum := dispatchLagSamples(t, reg)
	if count != 1 || sum < 120 || sum > 180 {
		t.Fatalf("dispatch lag samples=%d sum=%v, want one sample of about 120s", count, sum)
	}
	final, err := store.GetWorkflowRun(ctx, run.ID)
	if err != nil || final.Status != state.WorkflowRunStatusSucceeded {
		t.Fatalf("run = %+v err=%v, want succeeded", final, err)
	}
}
