package faas_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	faas "github.com/poyrazK/faas/sdk/go"
)

func workflowRequest(t *testing.T, name string, journal []faas.WorkflowJournalEntry, input any) []byte {
	t.Helper()
	rawInput, _ := json.Marshal(input)
	body, err := json.Marshal(faas.WorkflowInvocationRequest{Protocol: faas.WorkflowCodeProtocol, RunID: "run-1", Workflow: name, Input: rawInput, Invocation: len(journal) + 1, Journal: journal})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func invoke(t *testing.T, def faas.WorkflowDefinition, journal []faas.WorkflowJournalEntry, input any) *faas.WorkflowInvocationResponse {
	t.Helper()
	response, err := faas.RunWorkflowInvocation(context.Background(), []faas.WorkflowDefinition{def}, workflowRequest(t, def.Name, journal, input))
	if err != nil {
		t.Fatal(err)
	}
	return response
}

type order struct {
	ID string `json:"id"`
}

type charge struct {
	ID  string `json:"id"`
	Key string `json:"key"`
}

var orderWorkflow = faas.DefineWorkflow("order", func(ctx context.Context, wf *faas.Workflow, input json.RawMessage) (any, error) {
	in, err := faas.WorkflowDecode[order](input)
	if err != nil {
		return nil, err
	}
	paid, err := faas.WorkflowStep(wf, "charge", func(_ context.Context, sc faas.WorkflowStepContext) (charge, error) {
		return charge{ID: "ch_" + in.ID, Key: sc.IdempotencyKey}, nil
	})
	if err != nil {
		return nil, err
	}
	wf.Sleep("cool_off", 72*time.Hour)
	approval, ok, err := faas.WorkflowWaitForEvent[struct {
		OK bool `json:"ok"`
	}](wf, "approval", faas.WorkflowWaitOptions{Event: "approved", Timeout: 7 * 24 * time.Hour})
	if err != nil {
		return nil, err
	}
	if !ok {
		return map[string]string{"status": "expired", "charge": paid.ID}, nil
	}
	status := "refused"
	if approval.OK {
		status = "shipped"
	}
	return map[string]string{"status": status, "charge": paid.ID}, nil
})

func TestWorkflowFirstCallRunsFirstStepAndSuspends(t *testing.T) {
	response := invoke(t, orderWorkflow, nil, order{ID: "42"})
	if response.Protocol != faas.WorkflowCodeProtocol || response.Result.Status != "running" || len(response.Ops) != 1 {
		t.Fatalf("response %+v", response)
	}
	op := response.Ops[0]
	if op.Seq != 0 || op.Name != "charge" || op.Kind != "step" || op.Status != "succeeded" || string(op.Output) != `{"id":"ch_42","key":"workflow/run-1/charge"}` {
		t.Fatalf("op %+v output %s", op, op.Output)
	}
}

func TestWorkflowReplaysRecordedStepsAndReportsWaitsOnce(t *testing.T) {
	calls := 0
	def := faas.DefineWorkflow("counted", func(_ context.Context, wf *faas.Workflow, _ json.RawMessage) (any, error) {
		if _, err := faas.WorkflowStep(wf, "charge", func(context.Context, faas.WorkflowStepContext) (int, error) { calls++; return 1, nil }); err != nil {
			return nil, err
		}
		wf.Sleep("cool_off", 5*time.Second)
		return "done", nil
	})
	journal := []faas.WorkflowJournalEntry{{Seq: 0, Name: "charge", Kind: "step", Status: "succeeded", Attempt: 1, Output: json.RawMessage("1")}}
	response := invoke(t, def, journal, nil)
	if calls != 0 || !reflect.DeepEqual(response.Ops, []faas.WorkflowReportedOp{{Seq: 1, Name: "cool_off", Kind: "sleep", DurationMS: 5000}}) {
		t.Fatalf("calls=%d ops=%+v", calls, response.Ops)
	}
	waiting := invoke(t, def, append(journal, faas.WorkflowJournalEntry{Seq: 1, Name: "cool_off", Kind: "sleep", Status: "waiting"}), nil)
	if len(waiting.Ops) != 0 || waiting.Result.Status != "running" {
		t.Fatalf("waiting %+v", waiting)
	}
	done := invoke(t, def, append(journal, faas.WorkflowJournalEntry{Seq: 1, Name: "cool_off", Kind: "sleep", Status: "succeeded"}), nil)
	if done.Result.Status != "succeeded" || string(done.Result.Output) != `"done"` {
		t.Fatalf("done %+v", done.Result)
	}
}

func TestWorkflowParallelRunsStepsTogether(t *testing.T) {
	def := faas.DefineWorkflow("fanout", func(_ context.Context, wf *faas.Workflow, _ json.RawMessage) (any, error) {
		results := faas.WorkflowParallel(wf,
			faas.WorkflowParallelStep{Name: "slow", Run: func(context.Context, faas.WorkflowStepContext) (any, error) {
				time.Sleep(20 * time.Millisecond)
				return "a", nil
			}},
			faas.WorkflowParallelStep{Name: "fast", Run: func(context.Context, faas.WorkflowStepContext) (any, error) { return "b", nil }},
		)
		a, _ := faas.WorkflowDecode[string](results[0].Output)
		b, _ := faas.WorkflowDecode[string](results[1].Output)
		return a + b, nil
	})
	first := invoke(t, def, nil, nil)
	if len(first.Ops) != 2 || first.Ops[0].Name != "slow" || first.Ops[1].Name != "fast" || string(first.Ops[0].Output) != `"a"` {
		t.Fatalf("first %+v", first.Ops)
	}
	var journal []faas.WorkflowJournalEntry
	for _, op := range first.Ops {
		journal = append(journal, faas.WorkflowJournalEntry{Seq: op.Seq, Name: op.Name, Kind: "step", Status: "succeeded", Attempt: 1, Output: op.Output})
	}
	if done := invoke(t, def, journal, nil); string(done.Result.Output) != `"ab"` {
		t.Fatalf("done %+v", done.Result)
	}
}

func TestWorkflowRepeatedNamesAndChangedCode(t *testing.T) {
	loop := faas.DefineWorkflow("loop", func(_ context.Context, wf *faas.Workflow, _ json.RawMessage) (any, error) {
		for i := 0; i < 3; i++ {
			if _, err := faas.WorkflowStep(wf, "poll", func(context.Context, faas.WorkflowStepContext) (int, error) { return i, nil }); err != nil {
				return nil, err
			}
		}
		return "ok", nil
	})
	result, err := faas.RunWorkflowLocally(context.Background(), loop, nil, faas.WorkflowLocalRunOptions{})
	if err != nil || result.Status != "succeeded" {
		t.Fatalf("result %+v err %v", result, err)
	}
	var names []string
	for _, entry := range result.Journal {
		names = append(names, entry.Name)
	}
	if !reflect.DeepEqual(names, []string{"poll", "poll#2", "poll#3"}) {
		t.Fatalf("names %v", names)
	}
	changed := faas.DefineWorkflow("order", func(_ context.Context, wf *faas.Workflow, _ json.RawMessage) (any, error) {
		_, err := faas.WorkflowStep(wf, "charge_v2", func(context.Context, faas.WorkflowStepContext) (int, error) { return 1, nil })
		return nil, err
	})
	response := invoke(t, changed, []faas.WorkflowJournalEntry{{Seq: 0, Name: "charge", Kind: "step", Status: "succeeded", Attempt: 1, Output: json.RawMessage("1")}}, nil)
	if response.Result.Status != "running" || !reflect.DeepEqual(response.Ops, []faas.WorkflowReportedOp{{Seq: 0, Name: "charge_v2", Kind: "step", Status: "succeeded"}}) {
		t.Fatalf("changed code must be reported for the engine to refuse: %+v", response)
	}
}

func TestWorkflowFailuresRetryThenReturnStepError(t *testing.T) {
	flaky := faas.DefineWorkflow("flaky", func(_ context.Context, wf *faas.Workflow, _ json.RawMessage) (any, error) {
		_, err := faas.WorkflowStep(wf, "call", func(_ context.Context, sc faas.WorkflowStepContext) (int, error) {
			return 0, fmt.Errorf("boom %d", sc.Attempt)
		}, faas.WithStepRetry(2, "exponential"))
		var stepErr *faas.WorkflowStepError
		if errors.As(err, &stepErr) {
			return map[string]any{"recovered": stepErr.Message, "attempts": stepErr.Attempts}, nil
		}
		return nil, err
	})
	first := invoke(t, flaky, nil, nil)
	if op := first.Ops[0]; op.Status != "failed" || op.Error != "boom 1" || op.Retry == nil || op.Retry.MaxAttempts != 2 || op.Retry.Backoff != "exponential" {
		t.Fatalf("first %+v", first.Ops)
	}
	second := invoke(t, flaky, []faas.WorkflowJournalEntry{{Seq: 0, Name: "call", Kind: "step", Status: "retrying", Attempt: 1, Error: "boom 1"}}, nil)
	if second.Ops[0].Error != "boom 2" {
		t.Fatalf("second %+v", second.Ops)
	}
	local, err := faas.RunWorkflowLocally(context.Background(), flaky, nil, faas.WorkflowLocalRunOptions{})
	if err != nil || string(local.Output) != `{"attempts":2,"recovered":"boom 2"}` {
		t.Fatalf("local %+v err %v", local, err)
	}
}

func TestWorkflowNonRetryablePanicsAndOpaqueResults(t *testing.T) {
	strict := faas.DefineWorkflow("strict", func(_ context.Context, wf *faas.Workflow, _ json.RawMessage) (any, error) {
		_, err := faas.WorkflowStep(wf, "reject", func(context.Context, faas.WorkflowStepContext) (int, error) {
			return 0, faas.NonRetryable(errors.New("card declined"))
		})
		return nil, err
	})
	if op := invoke(t, strict, nil, nil).Ops[0]; !op.NonRetryable || op.Error != "card declined" {
		t.Fatalf("non-retryable %+v", op)
	}
	opaque := faas.DefineWorkflow("opaque", func(_ context.Context, wf *faas.Workflow, _ json.RawMessage) (any, error) {
		_, err := faas.WorkflowStep(wf, "value", func(context.Context, faas.WorkflowStepContext) (func(), error) { return func() {}, nil })
		return nil, err
	})
	if op := invoke(t, opaque, nil, nil).Ops[0]; op.Status != "failed" || !op.NonRetryable {
		t.Fatalf("opaque %+v", op)
	}
	panicky := faas.DefineWorkflow("panicky", func(_ context.Context, wf *faas.Workflow, _ json.RawMessage) (any, error) {
		_, err := faas.WorkflowStep(wf, "explode", func(context.Context, faas.WorkflowStepContext) (int, error) { panic("kaboom") })
		return nil, err
	})
	if op := invoke(t, panicky, nil, nil).Ops[0]; op.Status != "failed" || !strings.Contains(op.Error, "kaboom") {
		t.Fatalf("step panic %+v", op)
	}
	broken := faas.DefineWorkflow("broken", func(context.Context, *faas.Workflow, json.RawMessage) (any, error) {
		return nil, errors.New("bad input")
	})
	if result := invoke(t, broken, nil, nil).Result; result.Status != "failed" || result.Error != "bad input" {
		t.Fatalf("broken %+v", result)
	}
}

func TestWorkflowEventsResolveInOrderAndTimeOut(t *testing.T) {
	approved, err := faas.RunWorkflowLocally(context.Background(), orderWorkflow, order{ID: "7"}, faas.WorkflowLocalRunOptions{Events: map[string][]any{"approved": {map[string]bool{"ok": true}}}})
	if err != nil || string(approved.Output) != `{"charge":"ch_7","status":"shipped"}` {
		t.Fatalf("approved %+v err %v", approved, err)
	}
	expired, err := faas.RunWorkflowLocally(context.Background(), orderWorkflow, order{ID: "7"}, faas.WorkflowLocalRunOptions{})
	if err != nil || string(expired.Output) != `{"charge":"ch_7","status":"expired"}` {
		t.Fatalf("expired %+v err %v", expired, err)
	}
	journal := []faas.WorkflowJournalEntry{
		{Seq: 0, Name: "charge", Kind: "step", Status: "succeeded", Attempt: 1, Output: json.RawMessage(`{"id":"ch_7"}`)},
		{Seq: 1, Name: "cool_off", Kind: "sleep", Status: "succeeded"},
	}
	response := invoke(t, orderWorkflow, journal, order{ID: "7"})
	want := []faas.WorkflowReportedOp{{Seq: 2, Name: "approval", Kind: "event", Event: "approved", TimeoutMS: 7 * 24 * 3600 * 1000}}
	if !reflect.DeepEqual(response.Ops, want) {
		t.Fatalf("ops %+v", response.Ops)
	}
}

func TestWorkflowOverflowingResultsFailWithoutRetry(t *testing.T) {
	big := strings.Repeat("x", 600*1024)
	huge := faas.DefineWorkflow("huge", func(_ context.Context, wf *faas.Workflow, _ json.RawMessage) (any, error) {
		faas.WorkflowParallel(wf,
			faas.WorkflowParallelStep{Name: "a", Run: func(context.Context, faas.WorkflowStepContext) (any, error) { return big, nil }},
			faas.WorkflowParallelStep{Name: "b", Run: func(context.Context, faas.WorkflowStepContext) (any, error) { return big, nil }},
		)
		return nil, nil
	})
	response := invoke(t, huge, nil, nil)
	if response.Ops[0].Status != "succeeded" || response.Ops[1].Status != "failed" || !response.Ops[1].NonRetryable || response.Ops[1].Output != nil {
		t.Fatalf("ops %+v", response.Ops[1])
	}
	raw, _ := json.Marshal(response)
	if len(raw) > faas.WorkflowResponseMaxBytes {
		t.Fatalf("answer is %d bytes", len(raw))
	}
}

func TestServeWorkflowsOnlyAcceptsGregaleCalls(t *testing.T) {
	handler := faas.ServeWorkflows(orderWorkflow)
	body := workflowRequest(t, "order", nil, order{ID: "1"})
	call := func(h http.Handler, method string, headers map[string]string, payload []byte) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/order", strings.NewReader(string(payload)))
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := call(handler, http.MethodPost, nil, body); rec.Code != http.StatusForbidden {
		t.Fatalf("unverified caller: %d", rec.Code)
	}
	rec := call(handler, http.MethodPost, map[string]string{"X-Faas-Workflow-Mode": "code", "X-Faas-Workflow-Run-Id": "run-1"}, body)
	var response faas.WorkflowInvocationResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &response) != nil || len(response.Ops) != 1 {
		t.Fatalf("platform call: %d %s", rec.Code, rec.Body.String())
	}
	open := faas.ServeWorkflowsWithOptions(faas.ServeWorkflowsOptions{AllowUnverifiedCallers: true}, orderWorkflow)
	if rec := call(open, http.MethodPost, nil, workflowRequest(t, "missing", nil, nil)); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown workflow: %d", rec.Code)
	}
	if rec := call(handler, http.MethodGet, nil, nil); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET: %d", rec.Code)
	}
}
