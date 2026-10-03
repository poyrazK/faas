// adr: 489
package sched

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/outbound"
	"github.com/onebox-faas/faas/pkg/state"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type workflowRoundTripper func(*http.Request) (*http.Response, error)

func (f workflowRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestWorkflowOutboundRetryAfterIsBounded(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		raw  string
		want time.Time
	}{
		{name: "delta seconds", raw: "7", want: now.Add(7 * time.Second)},
		{name: "large unsigned delta is capped before conversion", raw: "18446744073709551615", want: now.Add(time.Hour)},
		{name: "negative delta is ignored", raw: "-1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := workflowOutboundRetryAfter(tc.raw, now); !got.Equal(tc.want) {
				t.Fatalf("workflowOutboundRetryAfter(%q) = %s, want %s", tc.raw, got, tc.want)
			}
		})
	}
}

type outboundLeaseStub struct{ token atomic.Pointer[string] }

func (s *outboundLeaseStub) GetWorkflowOutboundAttempt(context.Context, string, string, int) (state.WorkflowOutboundAttempt, error) {
	token := s.token.Load()
	if token == nil {
		return state.WorkflowOutboundAttempt{}, state.ErrWorkflowNotRunning
	}
	return state.WorkflowOutboundAttempt{AccountID: "00000000-0000-0000-0000-000000000001", AppID: "00000000-0000-0000-0000-000000000002", Token: *token}, nil
}
func (s *outboundLeaseStub) ValidateWorkflowOutboundBindings(context.Context, string, api.WorkflowSpec) error {
	return nil
}

func TestWorkflowOutboundExecutorOutputAndCancellation(t *testing.T) {
	store := &outboundLeaseStub{}
	nonce := uuid.NewString()
	store.token.Store(&nonce)
	identityCalls := 0
	capturedBody := ""
	key := ""
	executor := &workflowOutboundExecutor{store: store, mint: func(identity outbound.WorkflowIdentity, _, _, _ string, body []byte) (string, error) {
		identityCalls++
		if identity.AttemptToken != nonce {
			t.Error("wrong lease")
		}
		capturedBody = string(body)
		return "private-token", nil
	}, client: &http.Client{Transport: workflowRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != executionOutboundGatewayAddress || r.Header.Get(outbound.WorkflowIdentityHeader) != "private-token" {
			t.Error("wrong private transport")
		}
		key = r.Header.Get("Idempotency-Key")
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":17}`))}, nil
	})}}
	spec := api.WorkflowOutboundSpec{IntegrationID: uuid.NewString(), Method: "POST", Path: "/v1/items", IdempotencySupported: true}
	runID := uuid.NewString()
	status, output, _, err := executor.ExecuteOutboundStep(context.Background(), runID, "send", 1, spec, []byte(`{"email":"a@example.com"}`), time.Second)
	if err != nil || status != 200 || string(output) != `{"status":200,"body":{"id":17}}` || capturedBody == "" || identityCalls != 1 || key != workflowStepIdempotencyKey(runID, "send") {
		t.Fatalf("status=%d output=%s err=%v", status, output, err)
	}
	spec.Method = "GET"
	if _, _, _, err := executor.ExecuteOutboundStep(context.Background(), runID, "send", 2, spec, []byte(`{"must_not_send":true}`), time.Second); err != nil || capturedBody != "" {
		t.Fatalf("GET inherited a body: %s %v", capturedBody, err)
	}
	executor.client.Transport = workflowRoundTripper(func(r *http.Request) (*http.Response, error) {
		store.token.Store(nil)
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	started := time.Now()
	_, _, _, err = executor.ExecuteOutboundStep(context.Background(), runID, "send", 3, spec, nil, 5*time.Second)
	if err == nil || time.Since(started) > 3*time.Second {
		t.Fatalf("revocation did not cancel call: %v duration=%v", err, time.Since(started))
	}
}

type workflowOutboundRecorder struct {
	calls   int
	keys    []int
	input   [][]byte
	status  int
	retryAt time.Time
}

func (r *workflowOutboundRecorder) ExecuteOutboundStep(_ context.Context, _, _ string, attempt int, _ api.WorkflowOutboundSpec, input []byte, _ time.Duration) (int, []byte, time.Time, error) {
	r.calls++
	r.keys = append(r.keys, attempt)
	r.input = append(r.input, append([]byte(nil), input...))
	if r.calls == 1 && r.status != 0 {
		return r.status, nil, r.retryAt, nil
	}
	return 200, []byte(`{"status":200,"body":{"ok":true}}`), time.Time{}, nil
}

func TestWorkflowOutboundOrchestrationPersistsRetryAndInput(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	target := api.WorkflowOutboundSpec{IntegrationID: uuid.NewString(), Method: "POST", Path: "/v1/contacts", IdempotencySupported: true}
	spec := api.WorkflowSpec{Name: "crm", Steps: []api.WorkflowStepSpec{{Name: "send", Outbound: &target, Input: json.RawMessage(`{"email":"{{input.email}}"}`), Retry: &api.WorkflowRetrySpec{MaxAttempts: 2, Backoff: "exponential"}}}}
	snapshot, _ := json.Marshal(spec)
	run := &state.WorkflowRun{AppID: uuid.NewString(), WorkflowName: spec.Name, Input: json.RawMessage(`{"email":"a@example.com"}`), DefinitionSnapshot: snapshot}
	if err := store.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	provider := &workflowOutboundRecorder{status: 429, retryAt: time.Now().Add(1200 * time.Millisecond)}
	orchestrator := NewWorkflowOrchestrator(store, nil, nil, nil, nil).WithOutboundExecutor(provider)
	if err := orchestrator.DispatchTick(ctx); err != nil {
		t.Fatal(err)
	}
	parked, err := store.GetWorkflowRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	steps, _ := store.GetWorkflowSteps(ctx, run.ID)
	if provider.calls != 1 || steps[0].NextRetryAt == nil || steps[0].NextRetryAt.Before(provider.retryAt) || parked.ScheduledFor.Before(provider.retryAt) {
		t.Fatalf("Retry-After was not durably preserved: %+v %+v", parked, steps[0])
	}
	if err := orchestrator.DispatchTick(ctx); err != nil {
		t.Fatal(err)
	}
	if provider.calls != 1 {
		t.Fatal("retried before the deadline")
	}
	time.Sleep(time.Until(*steps[0].NextRetryAt) + 10*time.Millisecond)
	if err := orchestrator.DispatchTick(ctx); err != nil {
		t.Fatal(err)
	}
	final, _ := store.GetWorkflowRun(ctx, run.ID)
	attempts, _ := store.GetWorkflowStepAttempts(ctx, run.ID, "send")
	if final.Status != state.WorkflowRunStatusSucceeded || provider.calls != 2 || provider.keys[1] != 2 || string(provider.input[0]) != string(provider.input[1]) || len(attempts) != 2 || attempts[0].HTTPStatus == nil {
		t.Fatalf("retry result=%+v attempts=%+v inputs=%q", final, attempts, provider.input)
	}
}

func TestWorkflowOutboundRecoveryCannotExceedAttemptLimit(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	spec := api.WorkflowSpec{Name: "bounded", Steps: []api.WorkflowStepSpec{{Name: "send", Outbound: &api.WorkflowOutboundSpec{IntegrationID: uuid.NewString(), Method: "GET", Path: "/v1/items"}, Retry: &api.WorkflowRetrySpec{MaxAttempts: 1}}}}
	snapshot, _ := json.Marshal(spec)
	run := &state.WorkflowRun{AppID: uuid.NewString(), WorkflowName: spec.Name, DefinitionSnapshot: snapshot}
	if err := store.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateWorkflowSteps(ctx, run.ID, []*state.WorkflowStep{{StepName: "send"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimNextDueWorkflowRun(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := store.StartWorkflowStep(ctx, run.ID, "send", 1, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err := store.RecoverWorkflowRun(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	provider := &workflowOutboundRecorder{}
	orchestrator := NewWorkflowOrchestrator(store, nil, nil, nil, nil).WithOutboundExecutor(provider)
	if err := orchestrator.DispatchTick(ctx); err != nil {
		t.Fatal(err)
	}
	final, err := store.GetWorkflowRun(ctx, run.ID)
	if err != nil || final.Status != state.WorkflowRunStatusFailed || provider.calls != 0 {
		t.Fatalf("recovery exceeded attempts: run=%+v provider_calls=%d err=%v", final, provider.calls, err)
	}
}

type workflowOutboundFunc func(context.Context, string, string, int, api.WorkflowOutboundSpec, []byte, time.Duration) (int, []byte, time.Time, error)

func (f workflowOutboundFunc) ExecuteOutboundStep(ctx context.Context, runID, step string, attempt int, spec api.WorkflowOutboundSpec, input []byte, timeout time.Duration) (int, []byte, time.Time, error) {
	return f(ctx, runID, step, attempt, spec, input, timeout)
}

func TestWorkflowOutboundObsoleteWorkerCannotRecoverReplacement(t *testing.T) {
	for _, status := range []int{200, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			spec := api.WorkflowSpec{Name: "replaced", Steps: []api.WorkflowStepSpec{{Name: "send", Outbound: &api.WorkflowOutboundSpec{IntegrationID: uuid.NewString(), Method: "GET", Path: "/v1/items"}}}}
			snapshot, _ := json.Marshal(spec)
			run := &state.WorkflowRun{AppID: uuid.NewString(), WorkflowName: spec.Name, DefinitionSnapshot: snapshot}
			if err := store.CreateWorkflowRun(ctx, run); err != nil {
				t.Fatal(err)
			}
			provider := workflowOutboundFunc(func(ctx context.Context, runID, step string, _ int, _ api.WorkflowOutboundSpec, input []byte, _ time.Duration) (int, []byte, time.Time, error) {
				if err := store.RecoverWorkflowRun(ctx, runID); err != nil {
					t.Fatal(err)
				}
				if _, err := store.ClaimNextDueWorkflowRun(ctx); err != nil {
					t.Fatal(err)
				}
				if _, err := store.StartWorkflowStep(ctx, runID, step, 2, input); err != nil {
					t.Fatal(err)
				}
				return status, []byte(`{"status":200,"body":{"stale":true}}`), time.Time{}, nil
			})
			orchestrator := NewWorkflowOrchestrator(store, nil, nil, nil, nil).WithOutboundExecutor(provider)
			if err := orchestrator.DispatchTick(ctx); err != nil {
				t.Fatal(err)
			}
			current, _ := store.GetWorkflowRun(ctx, run.ID)
			steps, _ := store.GetWorkflowSteps(ctx, run.ID)
			attempts, _ := store.GetWorkflowStepAttempts(ctx, run.ID, "send")
			if current.Status != state.WorkflowRunStatusRunning || steps[0].Status != state.WorkflowStepStatusRunning || steps[0].Attempt != 2 || len(attempts) != 2 || attempts[1].Status != state.WorkflowAttemptStatusRunning {
				t.Fatalf("obsolete worker disturbed replacement: run=%+v step=%+v attempts=%+v", current, steps[0], attempts)
			}
		})
	}
}
