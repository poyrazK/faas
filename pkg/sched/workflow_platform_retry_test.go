package sched_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// refusedByGateway is a scripted dispatch reply: the gateway refused the
// attempt before the app received it.
const refusedByGateway = 0

// scriptedDispatchEndpoint stands in for gatewayd-internal's dispatch
// endpoint. Each reply is refusedByGateway (a 503 problem, as traffic
// security answers when it cannot read its generations) or the status the
// app answered with, inside the endpoint's 200 envelope.
type scriptedDispatchEndpoint struct {
	mu      sync.Mutex
	replies []int
	calls   int
}

func (e *scriptedDispatchEndpoint) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	e.mu.Lock()
	reply := http.StatusOK
	if e.calls < len(e.replies) {
		reply = e.replies[e.calls]
	}
	e.calls++
	e.mu.Unlock()
	if reply == refusedByGateway {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"code":"traffic_revocation_unavailable","detail":"Retry shortly.","status":503}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(w, `{"state":"completed","status_code":%d,"result":{"ok":true}}`, reply)
}

func (e *scriptedDispatchEndpoint) dispatches() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

func newPlatformRetryOrchestrator(t *testing.T, store state.Store, endpoint *scriptedDispatchEndpoint) *sched.WorkflowOrchestrator {
	t.Helper()
	srv := httptest.NewServer(endpoint)
	t.Cleanup(srv.Close)
	return sched.NewWorkflowOrchestrator(store, sched.XWorkflowGatewaySynth(srv.Client(), srv.URL), nil, nil, nil)
}

// settleWorkflowRun dispatches a run, and each retry once it is due, until
// the run is terminal.
func settleWorkflowRun(t *testing.T, store state.Store, orch *sched.WorkflowOrchestrator, runID string) *state.WorkflowRun {
	t.Helper()
	ctx := context.Background()
	for range 8 {
		if err := orch.DispatchTick(ctx); err != nil {
			t.Fatalf("DispatchTick: %v", err)
		}
		run, err := store.GetWorkflowRun(ctx, runID)
		if err != nil {
			t.Fatalf("GetWorkflowRun: %v", err)
		}
		switch run.Status {
		case state.WorkflowRunStatusSucceeded, state.WorkflowRunStatusFailed, state.WorkflowRunStatusDead:
			return run
		}
		if wait := time.Until(run.ScheduledFor); wait > 0 {
			time.Sleep(wait + 10*time.Millisecond)
		}
	}
	t.Fatal("workflow run did not settle")
	return nil
}

func workflowStepNamed(t *testing.T, store state.Store, runID, name string) *state.WorkflowStep {
	t.Helper()
	steps, err := store.GetWorkflowSteps(context.Background(), runID)
	if err != nil {
		t.Fatalf("GetWorkflowSteps: %v", err)
	}
	for _, step := range steps {
		if step.StepName == name {
			return step
		}
	}
	t.Fatalf("step %q not found", name)
	return nil
}

// adr: 081 (amendment 2026-11-02) — a workflow step attempt Gregale refused
// before the app received it (a 5xx from the gateway's dispatch endpoint)
// never ends the step within max_attempts + api.WorkflowStepPlatformRetries;
// an attempt the app answered keeps the step's own policy. PulseWatch F4: a
// one-attempt step went dead on a 503 traffic_revocation_unavailable the app
// never saw.
func TestWorkflowStepRefusedBeforeDeliveryIsRetried(t *testing.T) {
	oneAttempt := &api.WorkflowRetrySpec{MaxAttempts: 1}
	cases := []struct {
		name         string
		spec         api.WorkflowSpec
		input        string
		step         string
		replies      []int
		wantRun      string
		wantStep     string
		wantAttempts int
	}{
		{
			name:         "refused then delivered",
			spec:         api.WorkflowSpec{Name: "charge", Steps: []api.WorkflowStepSpec{{Name: "charge", Run: "charge", Retry: oneAttempt}}},
			step:         "charge",
			replies:      []int{refusedByGateway, http.StatusOK},
			wantRun:      state.WorkflowRunStatusSucceeded,
			wantStep:     state.WorkflowStepStatusSucceeded,
			wantAttempts: 2,
		},
		{
			name:         "the app's own failure keeps its policy",
			spec:         api.WorkflowSpec{Name: "charge", Steps: []api.WorkflowStepSpec{{Name: "charge", Run: "charge", Retry: oneAttempt}}},
			step:         "charge",
			replies:      []int{refusedByGateway, http.StatusInternalServerError},
			wantRun:      state.WorkflowRunStatusDead,
			wantStep:     state.WorkflowStepStatusDead,
			wantAttempts: 2,
		},
		{
			name:         "refusals are bounded",
			spec:         api.WorkflowSpec{Name: "charge", Steps: []api.WorkflowStepSpec{{Name: "charge", Run: "charge", Retry: oneAttempt}}},
			step:         "charge",
			replies:      []int{refusedByGateway, refusedByGateway, refusedByGateway, http.StatusOK},
			wantRun:      state.WorkflowRunStatusDead,
			wantStep:     state.WorkflowStepStatusDead,
			wantAttempts: 1 + api.WorkflowStepPlatformRetries,
		},
		{
			// A for_each item re-checks its attempts before each one, so the
			// platform retry must pass that check.
			name: "for_each item refused then delivered",
			spec: api.WorkflowSpec{Name: "batch", Steps: []api.WorkflowStepSpec{{Name: "batch", ForEach: &api.WorkflowForEachSpec{
				Items: "input.items", Action: api.WorkflowForEachActionSpec{Run: "send", Retry: oneAttempt}}}}},
			input:        `{"items":["a"]}`,
			step:         api.WorkflowForEachItemName("batch", 0),
			replies:      []int{refusedByGateway, http.StatusOK},
			wantRun:      state.WorkflowRunStatusSucceeded,
			wantStep:     state.WorkflowStepStatusSucceeded,
			wantAttempts: 2,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sched.GuardOrchestrationStores(t, func(t *testing.T, store state.Store) {
				input := tc.input
				if input == "" {
					input = `{}`
				}
				run := sched.CreateGuardedRun(t, store, tc.spec, input)
				endpoint := &scriptedDispatchEndpoint{replies: tc.replies}
				orch := newPlatformRetryOrchestrator(t, store, endpoint)
				orch.SetPlatformRetryDelayForTest(time.Millisecond)

				final := settleWorkflowRun(t, store, orch, run.ID)
				step := workflowStepNamed(t, store, run.ID, tc.step)
				if final.Status != tc.wantRun || step.Status != tc.wantStep || step.Attempt != tc.wantAttempts || endpoint.dispatches() != tc.wantAttempts {
					stepErr := ""
					if step.Error != nil {
						stepErr = *step.Error
					}
					t.Fatalf("run %s, step %s at attempt %d after %d dispatches (error %q); want run %s, step %s at attempt %d",
						final.Status, step.Status, step.Attempt, endpoint.dispatches(), stepErr, tc.wantRun, tc.wantStep, tc.wantAttempts)
				}
			})
		})
	}
}

// adr: 081 (amendment 2026-11-02) — a refused attempt retries no sooner than
// api.WorkflowStepPlatformRetryDelay: the step's one-second backoff tends to
// meet the same refusal.
func TestWorkflowStepRefusedBeforeDeliveryWaitsThePlatformDelay(t *testing.T) {
	sched.GuardOrchestrationStores(t, func(t *testing.T, store state.Store) {
		spec := api.WorkflowSpec{Name: "charge", Steps: []api.WorkflowStepSpec{{Name: "charge", Run: "charge", Retry: &api.WorkflowRetrySpec{MaxAttempts: 3}}}}
		run := sched.CreateGuardedRun(t, store, spec, `{}`)
		orch := newPlatformRetryOrchestrator(t, store, &scriptedDispatchEndpoint{replies: []int{refusedByGateway}})
		start := time.Now().UTC().Truncate(time.Millisecond)
		if err := orch.DispatchTick(context.Background()); err != nil {
			t.Fatalf("DispatchTick: %v", err)
		}
		step := workflowStepNamed(t, store, run.ID, "charge")
		if step.Status != state.WorkflowStepStatusPending || step.NextRetryAt == nil || step.NextRetryAt.Before(start.Add(api.WorkflowStepPlatformRetryDelay)) {
			t.Fatalf("refused step %s retries at %v; want pending, no sooner than %v", step.Status, step.NextRetryAt, start.Add(api.WorkflowStepPlatformRetryDelay))
		}
	})
}
