// adr: 437
package sched

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/audit"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

type foreachExecutor struct {
	inputs     [][]byte
	keys       []string
	names      []string
	failSecond bool
	retryFirst bool
	oversized  bool
}

func (e *foreachExecutor) ExecuteStep(_ context.Context, _, path, _ string, headers map[string]string, input []byte, _ time.Duration) (int, []byte, error) {
	if path == "/summary" {
		return 200, slices.Clone(input), nil
	}
	e.inputs = append(e.inputs, slices.Clone(input))
	e.keys = append(e.keys, headers["Idempotency-Key"])
	e.names = append(e.names, headers["X-Faas-Workflow-Step"])
	if e.retryFirst && len(e.inputs) == 1 {
		return 503, []byte(`{"private":"must not enter errors"}`), nil
	}
	if e.failSecond && len(e.inputs) == 2 {
		return 400, []byte(`{"private":"must not enter errors"}`), nil
	}
	if e.oversized {
		return 200, []byte(`"` + strings.Repeat("x", int(api.WorkflowForEachMaxOutputBytes)) + `"`), nil
	}
	return 200, slices.Clone(input), nil
}

func foreachOrchestrationSpec() api.WorkflowSpec {
	return api.WorkflowSpec{Name: "batch", Steps: []api.WorkflowStepSpec{
		{Name: "summary", Run: "summary", DependsOn: []string{"batch"}, Input: json.RawMessage(`"{{steps.batch.output}}"`)},
		{Name: "batch", ForEach: &api.WorkflowForEachSpec{Items: "input.items", Action: api.WorkflowForEachActionSpec{Run: "send", Input: json.RawMessage(`{"item":"{{input.item}}","index":"{{input.index}}"}`), Retry: &api.WorkflowRetrySpec{MaxAttempts: 2}}}},
	}}
}

func TestWorkflowForEachExecutesInOrderAndCollectsResults(t *testing.T) {
	for _, items := range []string{`[{"n":9007199254740993},true,null]`, `[]`} {
		t.Run(items, func(t *testing.T) {
			guardOrchestrationStores(t, func(t *testing.T, store state.Store) {
				ctx := context.Background()
				spec := foreachOrchestrationSpec()
				if _, err := api.ValidateWorkflowDAG(spec, api.PlanHobby); err != nil {
					t.Fatal(err)
				}
				run := createGuardedRun(t, store, spec, `{"items":`+items+`}`)
				executor := &foreachExecutor{}
				if err := NewWorkflowOrchestrator(store, executor, nil, nil, nil).DispatchTick(ctx); err != nil {
					t.Fatal(err)
				}
				final, err := store.GetWorkflowRun(ctx, run.ID)
				want := `[]`
				if items != "[]" {
					want = `[{"index":0,"item":{"n":9007199254740993}},{"index":1,"item":true},{"index":2,"item":null}]`
				}
				if err != nil || final.Status != state.WorkflowRunStatusSucceeded || !workflowJSONEqual(final.Output, []byte(want)) {
					t.Fatalf("ordered result: %+v %v", final, err)
				}
				for i, name := range executor.names {
					if name != api.WorkflowForEachItemName("batch", i) || executor.keys[i] != workflowStepIdempotencyKey(run.ID, name) {
						t.Fatal("item identity/key unstable")
					}
				}
				steps, err := store.GetWorkflowSteps(ctx, run.ID)
				if err != nil {
					t.Fatal(err)
				}
				for _, step := range steps {
					if step.StepName == "batch" && (step.Attempt != 0 || step.ForEachCount == nil) {
						t.Fatalf("parent allocated attempts: %+v", step)
					}
				}
			})
		})
	}
}

func TestWorkflowForEachRetryResumesItemWithSameInputAndKey(t *testing.T) {
	guardOrchestrationStores(t, func(t *testing.T, store state.Store) {
		ctx := context.Background()
		run := createGuardedRun(t, store, foreachOrchestrationSpec(), `{"items":["{{input.secret}}",2]}`)
		executor := &foreachExecutor{retryFirst: true}
		orchestrator := NewWorkflowOrchestrator(store, executor, nil, nil, nil)
		if err := orchestrator.DispatchTick(ctx); err != nil {
			t.Fatal(err)
		}
		steps, err := store.GetWorkflowSteps(ctx, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		var retryAt *time.Time
		for _, step := range steps {
			if step.ForEachParent != nil && step.ForEachIndex != nil && *step.ForEachIndex == 0 {
				retryAt = step.NextRetryAt
				if step.Error != nil && strings.Contains(*step.Error, "private") {
					t.Fatal("item failure body leaked")
				}
			}
		}
		if retryAt == nil || len(executor.inputs) != 1 {
			t.Fatalf("retry deadline missing or subsequent item ran: %v", executor.names)
		}
		if err := store.RecoverWorkflowRun(ctx, run.ID); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Until(*retryAt) + time.Millisecond)
		if err := orchestrator.DispatchTick(ctx); err != nil {
			t.Fatal(err)
		}
		final, err := store.GetWorkflowRun(ctx, run.ID)
		if err != nil || final.Status != state.WorkflowRunStatusSucceeded || len(executor.inputs) != 3 || !workflowJSONEqual(executor.inputs[0], executor.inputs[1]) || executor.keys[0] != executor.keys[1] || executor.keys[1] == executor.keys[2] {
			t.Fatalf("retry changed item or key: %+v inputs=%s keys=%v %v", final, executor.inputs, executor.keys, err)
		}
	})
}

func TestWorkflowForEachFailsWithoutDispatchingLaterItems(t *testing.T) {
	for _, mode := range []string{"failed item", "output limit", "invalid list", "false guard"} {
		t.Run(mode, func(t *testing.T) {
			guardOrchestrationStores(t, func(t *testing.T, store state.Store) {
				ctx := context.Background()
				spec := foreachOrchestrationSpec()
				input := `{"items":[0,1,2]}`
				executor := &foreachExecutor{failSecond: mode == "failed item", oversized: mode == "output limit"}
				if mode == "invalid list" {
					input = `{"items":null}`
				}
				if mode == "false guard" {
					spec.Steps[1].When = &api.WorkflowGuardSpec{Ref: "input.active", Op: "eq", Value: json.RawMessage(`true`)}
				}
				run := createGuardedRun(t, store, spec, input)
				if err := NewWorkflowOrchestrator(store, executor, nil, nil, nil).DispatchTick(ctx); err != nil {
					t.Fatal(err)
				}
				final, err := store.GetWorkflowRun(ctx, run.ID)
				if err != nil {
					t.Fatal(err)
				}
				wantCalls := 2
				if mode == "output limit" {
					wantCalls = 1
				}
				if mode == "invalid list" || mode == "false guard" {
					wantCalls = 0
				}
				if len(executor.inputs) != wantCalls {
					t.Fatalf("wrong items dispatched: %v", executor.names)
				}
				if mode == "false guard" {
					if final.Status != state.WorkflowRunStatusSucceeded {
						t.Fatalf("inactive batch stranded: %+v", final)
					}
				} else if final.Status != state.WorkflowRunStatusFailed && final.Status != state.WorkflowRunStatusDead {
					t.Fatalf("failed batch hid error: %+v", final)
				}
			})
		})
	}
}

type foreachCancellingExecutor struct {
	store state.Store
	runID string
	calls int
}

func (e *foreachCancellingExecutor) ExecuteStep(ctx context.Context, _, _, _ string, _ map[string]string, _ []byte, _ time.Duration) (int, []byte, error) {
	e.calls++
	if _, err := e.store.CancelWorkflowRun(ctx, e.runID, "cancel"); err != nil {
		return 0, nil, err
	}
	select {
	case <-ctx.Done():
		return 0, nil, ctx.Err()
	case <-time.After(3 * time.Second):
		return 0, nil, fmt.Errorf("item request was not cancelled")
	}
}

func TestWorkflowForEachCancellationStopsActiveAppRequest(t *testing.T) {
	guardOrchestrationStores(t, func(t *testing.T, store state.Store) {
		ctx := context.Background()
		run := createGuardedRun(t, store, foreachOrchestrationSpec(), `{"items":[0,1]}`)
		executor := &foreachCancellingExecutor{store: store, runID: run.ID}
		if err := NewWorkflowOrchestrator(store, executor, nil, nil, nil).DispatchTick(ctx); err != nil {
			t.Fatal(err)
		}
		final, err := store.GetWorkflowRun(ctx, run.ID)
		if err != nil || final.Status != state.WorkflowRunStatusFailed || executor.calls != 1 {
			t.Fatalf("cancelled iteration continued: %+v calls=%d %v", final, executor.calls, err)
		}
	})
}

type foreachOutboundRecorder struct {
	identities []string
	inputs     []json.RawMessage
}

func (e *foreachOutboundRecorder) ExecuteOutboundStep(_ context.Context, runID, name string, attempt int, target api.WorkflowOutboundSpec, input []byte, _ time.Duration) (int, []byte, time.Time, error) {
	if attempt != 1 || target.Method != "POST" || target.Path != "/send" {
		return 0, nil, time.Time{}, fmt.Errorf("unexpected item action")
	}
	e.identities = append(e.identities, workflowStepIdempotencyKey(runID, name))
	e.inputs = append(e.inputs, slices.Clone(input))
	output, err := json.Marshal(struct {
		Status int             `json:"status"`
		Body   json.RawMessage `json:"body"`
	}{200, input})
	return 200, output, time.Time{}, err
}

func TestWorkflowForEachUsesNativeOutboundActionAndCollectsProviderResults(t *testing.T) {
	guardOrchestrationStores(t, func(t *testing.T, store state.Store) {
		ctx := context.Background()
		spec := api.WorkflowSpec{Name: "provider_batch", Steps: []api.WorkflowStepSpec{{Name: "batch", ForEach: &api.WorkflowForEachSpec{
			Items: "input.items", Action: api.WorkflowForEachActionSpec{Outbound: &api.WorkflowOutboundSpec{IntegrationID: "00000000-0000-0000-0000-000000000001", Method: "POST", Path: "/send", IdempotencySupported: true}},
		}}}}
		if _, err := api.ValidateWorkflowDAG(spec, api.PlanHobby); err != nil {
			t.Fatal(err)
		}
		run := createGuardedRun(t, store, spec, `{"items":[{"n":9007199254740993},{"literal":"{{input.secret}}"}]}`)
		provider := &foreachOutboundRecorder{}
		if err := NewWorkflowOrchestrator(store, nil, nil, nil, nil).WithOutboundExecutor(provider).DispatchTick(ctx); err != nil {
			t.Fatal(err)
		}
		final, err := store.GetWorkflowRun(ctx, run.ID)
		want := `[{"status":200,"body":{"n":9007199254740993}},{"status":200,"body":{"literal":"{{input.secret}}"}}]`
		if err != nil || final.Status != state.WorkflowRunStatusSucceeded || !workflowJSONEqual(final.Output, []byte(want)) || len(provider.identities) != 2 {
			t.Fatalf("native iteration result: %+v calls=%v %v", final, provider.identities, err)
		}
		for i, identity := range provider.identities {
			if identity != workflowStepIdempotencyKey(run.ID, api.WorkflowForEachItemName("batch", i)) {
				t.Fatal("native item identity changed")
			}
		}
	})
}

func TestWorkflowForEachRunsMaximumBatch(t *testing.T) {
	guardOrchestrationStores(t, func(t *testing.T, store state.Store) {
		items := make([]int, api.WorkflowForEachMaxItems)
		for index := range items {
			items[index] = index
		}
		raw, err := json.Marshal(items)
		if err != nil {
			t.Fatal(err)
		}
		run := createGuardedRun(t, store, foreachOrchestrationSpec(), `{"items":`+string(raw)+`}`)
		executor := &foreachExecutor{}
		ctx := context.Background()
		if err := NewWorkflowOrchestrator(store, executor, nil, nil, nil).DispatchTick(ctx); err != nil {
			t.Fatal(err)
		}
		final, err := store.GetWorkflowRun(ctx, run.ID)
		var output []struct {
			Item  int `json:"item"`
			Index int `json:"index"`
		}
		if err != nil || final.Status != state.WorkflowRunStatusSucceeded || len(executor.inputs) != api.WorkflowForEachMaxItems || json.Unmarshal(final.Output, &output) != nil || len(output) != api.WorkflowForEachMaxItems {
			t.Fatalf("maximum batch failed: status=%s calls=%d %v", final.Status, len(executor.inputs), err)
		}
		for index, value := range output {
			if value.Item != index || value.Index != index {
				t.Fatalf("item order changed at %d: %+v", index, value)
			}
		}
	})
}

func TestWorkflowForEachPreservesOutboundAuditPolicyForAppItems(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	spec := api.WorkflowSpec{Name: "batch", Steps: []api.WorkflowStepSpec{
		{Name: "lookup", Outbound: &api.WorkflowOutboundSpec{IntegrationID: "00000000-0000-0000-0000-000000000001", Method: "POST", Path: "/send", IdempotencySupported: true}, Input: json.RawMessage(`"provider-result"`)},
		{Name: "send", DependsOn: []string{"lookup"}, ForEach: &api.WorkflowForEachSpec{Items: "input.items", Action: api.WorkflowForEachActionSpec{Run: "send", Input: json.RawMessage(`"{{steps.lookup.output.body}}"`)}}},
	}}
	if _, err := api.ValidateWorkflowDAG(spec, api.PlanHobby); err != nil {
		t.Fatal(err)
	}
	run := createGuardedRun(t, store, spec, `{"items":[0]}`)
	orchestrator := NewWorkflowOrchestrator(store, &foreachExecutor{}, audit.New(store, slog.Default(), nil, "schedd"), nil, nil).WithOutboundExecutor(&foreachOutboundRecorder{})
	if err := orchestrator.DispatchTick(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := store.ListEvents(ctx, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, row := range rows {
		if row.Kind != events.WorkflowStepSucceeded {
			continue
		}
		var payload struct {
			StepName string `json:"step_name"`
			Output   string `json:"output"`
		}
		if json.Unmarshal(row.Data, &payload) != nil {
			t.Fatal("invalid audit payload")
		}
		if payload.StepName == api.WorkflowForEachItemName("send", 0) {
			seen = true
			if payload.Output != "" || strings.Contains(string(row.Data), "provider-result") {
				t.Fatal("app item leaked provider output into audit history")
			}
		}
	}
	final, err := store.GetWorkflowRun(ctx, run.ID)
	if err != nil || final.Status != state.WorkflowRunStatusSucceeded || !seen {
		t.Fatalf("item audit missing: status=%s seen=%v %v", final.Status, seen, err)
	}
}
