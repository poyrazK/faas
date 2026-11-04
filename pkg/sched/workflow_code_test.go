// adr: 20261021204307
package sched_test

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workflowcode"
)

// errCodeSuspend unwinds a fake workflow function at its first new operation,
// the way the SDKs suspend an async function.
var errCodeSuspend = errors.New("suspend")

// codeTestWF is a minimal synchronous stand-in for the SDK replay engine.
type codeTestWF struct {
	journal []workflowcode.Entry
	seq     int
	ops     []workflowcode.ReportedOp
}

func (w *codeTestWF) claim(name string, kind workflowcode.Kind) (workflowcode.Entry, bool) {
	seq := w.seq
	w.seq++
	if seq >= len(w.journal) {
		return workflowcode.Entry{Seq: seq, Name: name, Kind: kind}, false
	}
	recorded := w.journal[seq]
	if recorded.Name != name || recorded.Kind != kind {
		w.ops = append(w.ops, workflowcode.ReportedOp{Seq: seq, Name: name, Kind: kind, Status: workflowcode.ResultSucceeded})
		panic(errCodeSuspend)
	}
	return recorded, true
}

func (w *codeTestWF) Step(name string, retry *api.WorkflowRetrySpec, fn func(attempt int) (any, error)) (json.RawMessage, error) {
	entry, recorded := w.claim(name, workflowcode.KindStep)
	switch {
	case recorded && entry.Status == workflowcode.EntrySucceeded:
		return entry.Output, nil
	case recorded && entry.Status == workflowcode.EntryFailed:
		return nil, errors.New(entry.Error)
	}
	op := workflowcode.ReportedOp{Seq: entry.Seq, Name: name, Kind: workflowcode.KindStep, Retry: retry}
	value, err := fn(entry.Attempt + 1)
	if err != nil {
		op.Status, op.Error = workflowcode.ResultFailed, err.Error()
	} else {
		op.Status = workflowcode.ResultSucceeded
		op.Output, _ = json.Marshal(value)
	}
	w.ops = append(w.ops, op)
	panic(errCodeSuspend)
}

func (w *codeTestWF) Sleep(name string, d time.Duration) {
	entry, recorded := w.claim(name, workflowcode.KindSleep)
	if recorded && entry.Status == workflowcode.EntrySucceeded {
		return
	}
	if !recorded {
		w.ops = append(w.ops, workflowcode.ReportedOp{Seq: entry.Seq, Name: name, Kind: workflowcode.KindSleep, DurationMS: d.Milliseconds()})
	}
	panic(errCodeSuspend)
}

func (w *codeTestWF) WaitForEvent(name, event string, timeout time.Duration) (json.RawMessage, bool) {
	entry, recorded := w.claim(name, workflowcode.KindEvent)
	if recorded && entry.Status == workflowcode.EntrySucceeded {
		return entry.Output, entry.TimedOut
	}
	if !recorded {
		w.ops = append(w.ops, workflowcode.ReportedOp{Seq: entry.Seq, Name: name, Kind: workflowcode.KindEvent, Event: event, TimeoutMS: timeout.Milliseconds()})
	}
	panic(errCodeSuspend)
}

type codeTestExecutor struct {
	run     func(wf *codeTestWF, call int) (any, error)
	status  int
	calls   int
	paths   []string
	headers []map[string]string
}

func (e *codeTestExecutor) ExecuteStep(_ context.Context, _, path, _ string, headers map[string]string, body []byte, _ time.Duration) (int, []byte, error) {
	e.calls++
	e.paths = append(e.paths, path)
	e.headers = append(e.headers, headers)
	if e.status != 0 {
		return e.status, []byte("handler crashed"), nil
	}
	var request workflowcode.Request
	if err := json.Unmarshal(body, &request); err != nil {
		return 400, []byte(err.Error()), nil //nolint:nilerr // a malformed request is an HTTP 400 from the handler, not a transport failure
	}
	if request.Protocol != api.WorkflowCodeProtocol {
		return 400, nil, nil
	}
	wf := &codeTestWF{journal: request.Journal}
	result := workflowcode.Result{Status: workflowcode.ResultRunning}
	func() {
		defer func() {
			if r := recover(); r != nil && r != errCodeSuspend { //nolint:errorlint // sentinel identity, not a wrapped error
				panic(r)
			}
		}()
		value, err := e.run(wf, e.calls)
		if err != nil {
			result = workflowcode.Result{Status: workflowcode.ResultFailed, Error: err.Error()}
			return
		}
		output, _ := json.Marshal(value)
		result = workflowcode.Result{Status: workflowcode.ResultSucceeded, Output: output}
	}()
	raw, _ := json.Marshal(workflowcode.Response{Protocol: api.WorkflowCodeProtocol, Ops: wf.ops, Result: result})
	return 200, raw, nil
}

func codeSpec(retry *api.WorkflowRetrySpec) api.WorkflowSpec {
	return api.WorkflowSpec{Name: "order", Code: &api.WorkflowCodeSpec{Run: "order", Retry: retry}}
}

func codeStepStatuses(t *testing.T, store state.Store, runID string) map[string]string {
	t.Helper()
	steps, err := store.GetWorkflowSteps(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	statuses := map[string]string{}
	for _, step := range steps {
		statuses[step.StepName] = step.Status
	}
	return statuses
}

func dispatchCode(t *testing.T, orchestrator *sched.WorkflowOrchestrator) {
	t.Helper()
	if err := orchestrator.DispatchTick(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestCodeWorkflowStepsSleepAndBufferedEvent(t *testing.T) {
	sched.GuardOrchestrationStores(t, func(t *testing.T, store state.Store) {
		ctx := context.Background()
		executor := &codeTestExecutor{run: func(wf *codeTestWF, _ int) (any, error) {
			charge, err := wf.Step("charge", nil, func(int) (any, error) { return map[string]string{"id": "ch_1"}, nil })
			if err != nil {
				return nil, err
			}
			wf.Sleep("cool_off", time.Second)
			approval, timedOut := wf.WaitForEvent("approval", "approved", time.Hour)
			if timedOut {
				return map[string]string{"status": "expired"}, nil
			}
			return map[string]json.RawMessage{"charge": charge, "approval": approval}, nil
		}}
		run := sched.CreateGuardedRun(t, store, codeSpec(nil), `{"order":1}`)
		orchestrator := sched.NewWorkflowOrchestrator(store, executor, nil, nil, nil)
		dispatchCode(t, orchestrator)
		parked, _ := store.GetWorkflowRun(ctx, run.ID)
		statuses := codeStepStatuses(t, store, run.ID)
		if parked.Status != state.WorkflowRunStatusAwaitingEvent || executor.calls != 2 || statuses["charge"] != state.WorkflowStepStatusSucceeded || statuses["cool_off"] != state.WorkflowStepStatusAwaitingEvent || statuses[api.WorkflowCodeDriverStep] != state.WorkflowStepStatusPending {
			t.Fatalf("after first dispatch: run=%s calls=%d steps=%v", parked.Status, executor.calls, statuses)
		}
		headers := executor.headers[0]
		if executor.paths[0] != "/order" || headers[api.WorkflowCodeModeHeader] != "code" || headers["X-Faas-Workflow-Run-Id"] != run.ID || headers["X-Faas-Workflow-Step"] != api.WorkflowCodeDriverStep {
			t.Fatalf("entry call path=%s headers=%v", executor.paths[0], headers)
		}
		// The event arrives while the run sleeps; it is kept for the later wait.
		if err := store.InsertWorkflowEvent(ctx, &state.WorkflowEvent{RunID: run.ID, EventName: "approved", Payload: json.RawMessage(`{"ok":true}`)}); err != nil {
			t.Fatal(err)
		}
		dispatchCode(t, orchestrator)
		if executor.calls != 2 {
			t.Fatalf("an unrelated wake must not call the code before the sleep ends: %d calls", executor.calls)
		}
		time.Sleep(1100 * time.Millisecond)
		dispatchCode(t, orchestrator)
		final, _ := store.GetWorkflowRun(ctx, run.ID)
		if final.Status != state.WorkflowRunStatusSucceeded || executor.calls != 4 {
			t.Fatalf("final run=%+v calls=%d", final, executor.calls)
		}
		if !sched.WorkflowJSONEqual(final.Output, []byte(`{"approval":{"ok":true},"charge":{"id":"ch_1"}}`)) {
			t.Fatalf("output=%s", final.Output)
		}
		statuses = codeStepStatuses(t, store, run.ID)
		if statuses["approval"] != state.WorkflowStepStatusSucceeded || statuses[api.WorkflowCodeDriverStep] != state.WorkflowStepStatusSucceeded {
			t.Fatalf("final steps=%v", statuses)
		}
		attempts, err := store.GetWorkflowStepAttempts(ctx, run.ID, api.WorkflowCodeDriverStep)
		if err != nil || len(attempts) != 4 || attempts[3].Status != state.WorkflowAttemptStatusSucceeded {
			t.Fatalf("driver attempts=%+v err=%v", attempts, err)
		}
	})
}

func TestCodeWorkflowStepRetriesThenThrowsIntoCode(t *testing.T) {
	sched.GuardOrchestrationStores(t, func(t *testing.T, store state.Store) {
		ctx := context.Background()
		executor := &codeTestExecutor{run: func(wf *codeTestWF, _ int) (any, error) {
			value, err := wf.Step("flaky", &api.WorkflowRetrySpec{MaxAttempts: 2, Backoff: "fixed"}, func(attempt int) (any, error) {
				if attempt == 1 {
					return nil, errors.New("upstream 503")
				}
				return attempt, nil
			})
			if err != nil {
				return nil, err
			}
			_, err = wf.Step("refund", &api.WorkflowRetrySpec{MaxAttempts: 1}, func(int) (any, error) { return nil, errors.New("card declined") })
			return map[string]any{"flaky": value, "recovered": err != nil && err.Error() == "card declined"}, nil
		}}
		run := sched.CreateGuardedRun(t, store, codeSpec(nil), `{}`)
		orchestrator := sched.NewWorkflowOrchestrator(store, executor, nil, nil, nil)
		dispatchCode(t, orchestrator)
		pending, _ := store.GetWorkflowRun(ctx, run.ID)
		if pending.Status != state.WorkflowRunStatusPending || executor.calls != 1 || !pending.ScheduledFor.After(time.Now()) {
			t.Fatalf("retry must wait for its back-off: %+v calls=%d", pending, executor.calls)
		}
		time.Sleep(1100 * time.Millisecond)
		dispatchCode(t, orchestrator)
		final, _ := store.GetWorkflowRun(ctx, run.ID)
		if final.Status != state.WorkflowRunStatusSucceeded || !sched.WorkflowJSONEqual(final.Output, []byte(`{"flaky":2,"recovered":true}`)) {
			t.Fatalf("final=%+v output=%s", final, final.Output)
		}
		attempts, err := store.GetWorkflowStepAttempts(ctx, run.ID, "flaky")
		if err != nil || len(attempts) != 2 || attempts[0].Status != state.WorkflowAttemptStatusRetrying || attempts[1].Status != state.WorkflowAttemptStatusSucceeded {
			t.Fatalf("flaky attempts=%+v err=%v", attempts, err)
		}
		if statuses := codeStepStatuses(t, store, run.ID); statuses["refund"] != state.WorkflowStepStatusFailed {
			t.Fatalf("refund must be failed: %v", statuses)
		}
	})
}

func TestCodeWorkflowEventOccurrencesAndNondeterminism(t *testing.T) {
	sched.GuardOrchestrationStores(t, func(t *testing.T, store state.Store) {
		ctx := context.Background()
		executor := &codeTestExecutor{run: func(wf *codeTestWF, _ int) (any, error) {
			first, _ := wf.WaitForEvent("tick", "tick", time.Hour)
			second, _ := wf.WaitForEvent("tick#2", "tick", time.Hour)
			return []json.RawMessage{first, second}, nil
		}}
		run := sched.CreateGuardedRun(t, store, codeSpec(nil), `{}`)
		for _, payload := range []string{`{"n":1}`, `{"n":2}`} {
			if err := store.InsertWorkflowEvent(ctx, &state.WorkflowEvent{RunID: run.ID, EventName: "tick", Payload: json.RawMessage(payload)}); err != nil {
				t.Fatal(err)
			}
			time.Sleep(2 * time.Millisecond)
		}
		dispatchCode(t, sched.NewWorkflowOrchestrator(store, executor, nil, nil, nil))
		final, _ := store.GetWorkflowRun(ctx, run.ID)
		if final.Status != state.WorkflowRunStatusSucceeded || !sched.WorkflowJSONEqual(final.Output, []byte(`[{"n":1},{"n":2}]`)) {
			t.Fatalf("each wait must consume its own event: %+v output=%s", final, final.Output)
		}

		changed := &codeTestExecutor{run: func(wf *codeTestWF, call int) (any, error) {
			name := "charge"
			if call > 1 {
				name = "charge_v2"
			}
			_, err := wf.Step(name, nil, func(int) (any, error) { return 1, nil })
			return nil, err
		}}
		drifted := sched.CreateGuardedRun(t, store, codeSpec(nil), `{}`)
		dispatchCode(t, sched.NewWorkflowOrchestrator(store, changed, nil, nil, nil))
		dead, _ := store.GetWorkflowRun(ctx, drifted.ID)
		if dead.Status != state.WorkflowRunStatusDead || dead.LastError == nil || !strings.Contains(*dead.LastError, "nondeterministic") {
			t.Fatalf("drifted run=%+v", dead)
		}
	})
}

// Each entry-handler call carries a different journal, so it needs its own
// Idempotency-Key: an app's idempotency middleware would otherwise replay the
// first call's answer and the engine would close the run as nondeterministic.
func TestCodeWorkflowDriverCallsUseDistinctIdempotencyKeys(t *testing.T) {
	sched.GuardOrchestrationStores(t, func(t *testing.T, store state.Store) {
		executor := &codeTestExecutor{run: func(wf *codeTestWF, _ int) (any, error) {
			if _, err := wf.Step("charge", nil, func(int) (any, error) { return 1, nil }); err != nil {
				return nil, err
			}
			return wf.Step("email", nil, func(int) (any, error) { return 2, nil })
		}}
		run := sched.CreateGuardedRun(t, store, codeSpec(nil), `{}`)
		dispatchCode(t, sched.NewWorkflowOrchestrator(store, executor, nil, nil, nil))
		if executor.calls != 3 {
			t.Fatalf("calls = %d, want 3", executor.calls)
		}
		for i, headers := range executor.headers {
			want := "workflow/" + run.ID + "/" + api.WorkflowCodeDriverStep + "/" + strconv.Itoa(i+1)
			if headers["Idempotency-Key"] != want {
				t.Fatalf("call %d Idempotency-Key = %q, want %q", i+1, headers["Idempotency-Key"], want)
			}
		}
	})
}

// A retrying step is sent as scheduled until its back-off elapses, even when
// another operation settling calls the code first.
func TestCodeWorkflowSendsRetriesThatAreNotDueAsScheduled(t *testing.T) {
	store := state.NewMemStore()
	var statuses []string
	executor := &codeTestExecutor{run: func(wf *codeTestWF, call int) (any, error) {
		if call == 2 {
			statuses = append(statuses, wf.journal[0].Status)
		}
		if _, err := wf.Step("flaky", &api.WorkflowRetrySpec{MaxAttempts: 3, Backoff: "fixed"}, func(attempt int) (any, error) {
			if attempt == 1 {
				return nil, errors.New("upstream 503")
			}
			return attempt, nil
		}); err != nil {
			return nil, err
		}
		return nil, nil
	}}
	run := sched.CreateGuardedRun(t, store, codeSpec(nil), `{}`)
	orchestrator := sched.NewWorkflowOrchestrator(store, executor, nil, nil, nil)
	dispatchCode(t, orchestrator)
	time.Sleep(1100 * time.Millisecond)
	dispatchCode(t, orchestrator)
	final, _ := store.GetWorkflowRun(context.Background(), run.ID)
	if final.Status != state.WorkflowRunStatusSucceeded || len(statuses) != 1 || statuses[0] != workflowcode.EntryRetrying {
		t.Fatalf("a due retry must be sent as retrying: run=%s statuses=%v", final.Status, statuses)
	}
	journal, err := workflowcode.BuildJournal([]workflowcode.Row{{Name: "flaky", Status: workflowcode.RowPending, Attempt: 1, NextRetry: ptrTime(time.Now().Add(time.Hour)), Input: workflowcode.EncodeDescriptor(workflowcode.Descriptor{Kind: workflowcode.KindStep})}})
	if err != nil {
		t.Fatal(err)
	}
	if sent := journal.Sent(time.Now()); sent[0].Status != workflowcode.EntryScheduled {
		t.Fatalf("a retry that is not due must be sent as scheduled: %+v", sent)
	}
}

func ptrTime(t time.Time) *time.Time { return &t }

// Stored error text must be valid UTF-8 without NUL, or PostgreSQL refuses
// the write and the failed call is retried at once instead of backing off.
func TestTruncateCodeErrorKeepsValidUTF8(t *testing.T) {
	long := strings.Repeat("a", api.WorkflowCodeErrorMaxBytes-1) + "ş" + "tail"
	if got := sched.TruncateCodeError(long); len(got) != api.WorkflowCodeErrorMaxBytes-1 || !utf8.ValidString(got) {
		t.Fatalf("cut through a rune: len=%d valid=%v", len(got), utf8.ValidString(got))
	}
	if got := sched.TruncateCodeError("bad \xff\xfe body\x00"); !utf8.ValidString(got) || strings.ContainsRune(got, 0) || !strings.HasPrefix(got, "bad ") {
		t.Fatalf("got %q", got)
	}
}

func TestCodeWorkflowEntryFailureRetriesWithBackoff(t *testing.T) {
	sched.GuardOrchestrationStores(t, func(t *testing.T, store state.Store) {
		ctx := context.Background()
		executor := &codeTestExecutor{status: 503}
		run := sched.CreateGuardedRun(t, store, codeSpec(nil), `{}`)
		dispatchCode(t, sched.NewWorkflowOrchestrator(store, executor, nil, nil, nil))
		pending, _ := store.GetWorkflowRun(ctx, run.ID)
		if pending.Status != state.WorkflowRunStatusPending || executor.calls != 1 || pending.ScheduledFor.Before(time.Now().Add(3*time.Second)) {
			t.Fatalf("entry failure must back off: %+v calls=%d", pending, executor.calls)
		}
		attempts, err := store.GetWorkflowStepAttempts(ctx, run.ID, api.WorkflowCodeDriverStep)
		if err != nil || len(attempts) != 1 || attempts[0].Status != state.WorkflowAttemptStatusRetrying || attempts[0].HTTPStatus == nil || *attempts[0].HTTPStatus != 503 {
			t.Fatalf("driver attempts=%+v err=%v", attempts, err)
		}
	})
}
