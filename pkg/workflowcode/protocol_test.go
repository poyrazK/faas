package workflowcode_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/workflowcode"
)

func row(name, status string, seq int, kind workflowcode.Kind, output string) workflowcode.Row {
	r := workflowcode.Row{Name: name, Status: status, Attempt: 1, Input: workflowcode.EncodeDescriptor(workflowcode.Descriptor{Seq: seq, Kind: kind, Event: "approved"})}
	if output != "" {
		r.Output = json.RawMessage(output)
	}
	return r
}

func limits() workflowcode.Limits {
	return workflowcode.DefaultLimits(30*24*time.Hour, api.WorkflowRetrySpec{MaxAttempts: 3, Backoff: "exponential"})
}

func response(status string, ops ...workflowcode.ReportedOp) workflowcode.Response {
	return workflowcode.Response{Protocol: api.WorkflowCodeProtocol, Ops: ops, Result: workflowcode.Result{Status: status}}
}

func TestDescriptorRoundTrip(t *testing.T) {
	raw := workflowcode.EncodeDescriptor(workflowcode.Descriptor{Seq: 4, Kind: workflowcode.KindSleep, DurationMS: 1500})
	if !strings.Contains(string(raw), `"gregale_code_op"`) {
		t.Fatalf("descriptor key missing: %s", raw)
	}
	got, ok := workflowcode.DecodeDescriptor(raw)
	if !ok || got.Seq != 4 || got.Kind != workflowcode.KindSleep || got.Duration() != 1500*time.Millisecond {
		t.Fatalf("decoded %+v %v", got, ok)
	}
	if _, ok := workflowcode.DecodeDescriptor(json.RawMessage(`{"order":1}`)); ok {
		t.Fatal("plain input decoded as a descriptor")
	}
}

func TestBuildJournalOrdersAndMapsStatuses(t *testing.T) {
	failed := "card declined"
	rows := []workflowcode.Row{
		row("wait", workflowcode.RowAwaitingEvent, 2, workflowcode.KindEvent, ""),
		row("charge", workflowcode.RowSucceeded, 0, workflowcode.KindStep, `{"id":"ch_1"}`),
		{Name: "refund", Status: workflowcode.RowPending, Attempt: 2, Error: &failed, Input: workflowcode.EncodeDescriptor(workflowcode.Descriptor{Seq: 1, Kind: workflowcode.KindStep})},
		row("late", workflowcode.RowSucceeded, 3, workflowcode.KindEvent, ""),
		{Name: api.WorkflowCodeDriverStep, Status: workflowcode.RowPending, Input: workflowcode.EncodeDescriptor(workflowcode.Descriptor{Kind: workflowcode.KindDriver})},
		{Name: "declarative", Status: workflowcode.RowSucceeded, Input: json.RawMessage(`{}`)},
	}
	journal, err := workflowcode.BuildJournal(rows)
	if err != nil {
		t.Fatal(err)
	}
	if journal.Driver == nil || len(journal.Entries) != 4 {
		t.Fatalf("journal %+v", journal)
	}
	want := []struct {
		name, status string
		timedOut     bool
	}{{"charge", "succeeded", false}, {"refund", "retrying", false}, {"wait", "waiting", false}, {"late", "succeeded", true}}
	for i, w := range want {
		e := journal.Entries[i]
		if e.Seq != i || e.Name != w.name || e.Status != w.status || e.TimedOut != w.timedOut {
			t.Fatalf("entry %d = %+v, want %+v", i, e, w)
		}
	}
	if journal.Entries[1].Error != failed || string(journal.Entries[0].Output) != `{"id":"ch_1"}` {
		t.Fatalf("error/output not carried: %+v", journal.Entries)
	}
}

func TestBuildJournalRejectsGaps(t *testing.T) {
	_, err := workflowcode.BuildJournal([]workflowcode.Row{row("a", workflowcode.RowSucceeded, 0, workflowcode.KindStep, "1"), row("c", workflowcode.RowSucceeded, 2, workflowcode.KindStep, "1")})
	if !errors.Is(err, workflowcode.ErrJournal) {
		t.Fatalf("err = %v", err)
	}
}

func TestValidateNewOperations(t *testing.T) {
	journal, _ := workflowcode.BuildJournal([]workflowcode.Row{row("charge", workflowcode.RowSucceeded, 0, workflowcode.KindStep, `1`)})
	plan, err := workflowcode.Validate(journal, response(workflowcode.ResultRunning,
		workflowcode.ReportedOp{Seq: 1, Name: "email", Kind: workflowcode.KindStep, Status: "succeeded", Output: json.RawMessage(`{"sent":true}`)},
		workflowcode.ReportedOp{Seq: 2, Name: "cool_off", Kind: workflowcode.KindSleep, DurationMS: 3 * 24 * 3600 * 1000},
		workflowcode.ReportedOp{Seq: 3, Name: "approval", Kind: workflowcode.KindEvent, Event: "approved"},
		workflowcode.ReportedOp{Seq: 4, Name: "approval#2", Kind: workflowcode.KindEvent, Event: "approved", TimeoutMS: 60_000},
	), limits())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Ops) != 4 || !plan.Ops[0].New || plan.Ops[0].Attempt != 1 {
		t.Fatalf("plan %+v", plan)
	}
	if plan.Ops[1].Descriptor.Duration() != 72*time.Hour || plan.Ops[1].Attempt != 0 {
		t.Fatalf("sleep descriptor %+v", plan.Ops[1])
	}
	if plan.Ops[2].Descriptor.Timeout() != 30*24*time.Hour || plan.Ops[2].Descriptor.Event != "approved" || plan.Ops[2].Descriptor.EventID != "" {
		t.Fatalf("event default timeout %+v", plan.Ops[2].Descriptor)
	}
	if plan.Ops[3].Descriptor.Timeout() != time.Minute || plan.Ops[3].Descriptor.EventID != "" {
		t.Fatalf("a new wait has consumed no event yet: %+v", plan.Ops[3].Descriptor)
	}
}

func TestValidateRetriesRecordedStep(t *testing.T) {
	journal, _ := workflowcode.BuildJournal([]workflowcode.Row{{Name: "charge", Status: workflowcode.RowPending, Attempt: 2, Input: workflowcode.EncodeDescriptor(workflowcode.Descriptor{Seq: 0, Kind: workflowcode.KindStep})}})
	plan, err := workflowcode.Validate(journal, response(workflowcode.ResultRunning,
		workflowcode.ReportedOp{Seq: 0, Name: "charge", Kind: workflowcode.KindStep, Status: "failed", Error: strings.Repeat("x", 10_000)},
	), limits())
	if err != nil {
		t.Fatal(err)
	}
	op := plan.Ops[0]
	if op.New || op.Attempt != 3 || op.Retry.MaxAttempts != 3 || len(op.Error) != api.WorkflowCodeErrorMaxBytes {
		t.Fatalf("retry plan %+v (error %d bytes)", op, len(op.Error))
	}
	if workflowcode.ShouldRetry(op) {
		t.Fatal("third of three attempts must not retry")
	}
	op.Attempt = 2
	if !workflowcode.ShouldRetry(op) {
		t.Fatal("second of three attempts must retry")
	}
	op.NonRetryable = true
	if workflowcode.ShouldRetry(op) {
		t.Fatal("non-retryable failure retried")
	}
}

func TestValidateRejectsNondeterminism(t *testing.T) {
	journal, _ := workflowcode.BuildJournal([]workflowcode.Row{
		row("charge", workflowcode.RowSucceeded, 0, workflowcode.KindStep, `1`),
		row("wait", workflowcode.RowAwaitingEvent, 1, workflowcode.KindSleep, ""),
	})
	cases := map[string]workflowcode.ReportedOp{
		"renamed":       {Seq: 0, Name: "charge_v2", Kind: workflowcode.KindStep, Status: "succeeded"},
		"kind changed":  {Seq: 0, Name: "charge", Kind: workflowcode.KindSleep, DurationMS: 5000},
		"settled again": {Seq: 0, Name: "charge", Kind: workflowcode.KindStep, Status: "succeeded"},
		"gap":           {Seq: 3, Name: "later", Kind: workflowcode.KindStep, Status: "succeeded"},
		"name reused":   {Seq: 2, Name: "charge", Kind: workflowcode.KindStep, Status: "succeeded"},
	}
	for name, op := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := workflowcode.Validate(journal, response(workflowcode.ResultRunning, op), limits()); !errors.Is(err, workflowcode.ErrNondeterministic) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestValidateRejectsMalformedAndOversized(t *testing.T) {
	empty := workflowcode.Journal{}
	protocol := map[string]workflowcode.Response{
		"protocol":   {Protocol: "other/v1", Result: workflowcode.Result{Status: "succeeded"}},
		"status":     {Protocol: api.WorkflowCodeProtocol, Result: workflowcode.Result{Status: "paused"}},
		"name":       response("running", workflowcode.ReportedOp{Seq: 0, Name: "$driver", Kind: workflowcode.KindStep, Status: "succeeded"}),
		"kind":       response("running", workflowcode.ReportedOp{Seq: 0, Name: "x", Kind: "fork"}),
		"step state": response("running", workflowcode.ReportedOp{Seq: 0, Name: "x", Kind: workflowcode.KindStep, Status: "running"}),
		"bad output": response("running", workflowcode.ReportedOp{Seq: 0, Name: "x", Kind: workflowcode.KindStep, Status: "succeeded", Output: json.RawMessage(`{`)}),
		"reserved":   response("running", workflowcode.ReportedOp{Seq: 0, Name: "x", Kind: workflowcode.KindEvent, Event: api.WorkflowCallbackEventName("00000000-0000-0000-0000-000000000000", "s")}),
		"stalled":    response("running"),
	}
	for name, resp := range protocol {
		t.Run(name, func(t *testing.T) {
			if _, err := workflowcode.Validate(empty, resp, limits()); !errors.Is(err, workflowcode.ErrProtocol) {
				t.Fatalf("err = %v", err)
			}
		})
	}
	small := limits()
	small.MaxOps = 1
	small.MaxWait = time.Hour
	oversized := map[string]workflowcode.Response{
		"ops": response("running",
			workflowcode.ReportedOp{Seq: 0, Name: "a", Kind: workflowcode.KindStep, Status: "succeeded"},
			workflowcode.ReportedOp{Seq: 1, Name: "b", Kind: workflowcode.KindStep, Status: "succeeded"}),
		"sleep":       response("running", workflowcode.ReportedOp{Seq: 0, Name: "a", Kind: workflowcode.KindSleep, DurationMS: 2 * 3600 * 1000}),
		"short sleep": response("running", workflowcode.ReportedOp{Seq: 0, Name: "a", Kind: workflowcode.KindSleep, DurationMS: 10}),
		"timeout":     response("running", workflowcode.ReportedOp{Seq: 0, Name: "a", Kind: workflowcode.KindEvent, Event: "e", TimeoutMS: 2 * 3600 * 1000}),
	}
	for name, resp := range oversized {
		t.Run(name, func(t *testing.T) {
			if _, err := workflowcode.Validate(empty, resp, small); !errors.Is(err, workflowcode.ErrLimit) {
				t.Fatalf("err = %v", err)
			}
		})
	}
	tiny := limits()
	tiny.JournalMaxBytes = 8
	if _, err := workflowcode.Validate(empty, response("running", workflowcode.ReportedOp{Seq: 0, Name: "a", Kind: workflowcode.KindStep, Status: "succeeded", Output: json.RawMessage(`"0123456789"`)}), tiny); !errors.Is(err, workflowcode.ErrLimit) {
		t.Fatalf("journal limit err = %v", err)
	}
}

func TestValidateTerminalResults(t *testing.T) {
	plan, err := workflowcode.Validate(workflowcode.Journal{}, workflowcode.Response{Protocol: api.WorkflowCodeProtocol, Result: workflowcode.Result{Status: "succeeded", Output: json.RawMessage(`{"ok":true}`)}}, limits())
	if err != nil || plan.Result.Status != "succeeded" {
		t.Fatalf("plan %+v err %v", plan, err)
	}
	if _, err := workflowcode.Validate(workflowcode.Journal{}, workflowcode.Response{Protocol: api.WorkflowCodeProtocol, Result: workflowcode.Result{Status: "failed", Error: "boom"}}, limits()); err != nil {
		t.Fatal(err)
	}
	waiting, _ := workflowcode.BuildJournal([]workflowcode.Row{row("w", workflowcode.RowAwaitingEvent, 0, workflowcode.KindEvent, "")})
	if _, err := workflowcode.Validate(waiting, response("running"), limits()); err != nil {
		t.Fatalf("a run still waiting may report no new operation: %v", err)
	}
}

func TestRetryDelay(t *testing.T) {
	exp := api.WorkflowRetrySpec{MaxAttempts: 25, Backoff: "exponential"}
	if workflowcode.RetryDelay(exp, 1) != time.Second || workflowcode.RetryDelay(exp, 4) != 8*time.Second || workflowcode.RetryDelay(exp, 20) != 256*time.Second {
		t.Fatal("exponential delays")
	}
	if workflowcode.RetryDelay(api.WorkflowRetrySpec{MaxAttempts: 3}, 9) != time.Second {
		t.Fatal("fixed delay")
	}
}
