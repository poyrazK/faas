package workflowcode_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/workflowcode"
)

func TestTerminalReplayCannotOmitHistory(t *testing.T) {
	for _, status := range []string{workflowcode.RowAwaitingEvent, workflowcode.RowSucceeded} {
		t.Run(status, func(t *testing.T) {
			journal, err := workflowcode.BuildJournal([]workflowcode.Row{{Name: "approval", Status: status,
				Input:  workflowcode.EncodeDescriptor(workflowcode.Descriptor{Seq: 0, Kind: workflowcode.KindEvent, Event: "approved", TimeoutMS: 1000}),
				Output: json.RawMessage(`true`),
			}})
			if err != nil {
				t.Fatal(err)
			}
			cursor := 0
			response := workflowcode.Response{Protocol: api.WorkflowCodeProtocol, Result: workflowcode.Result{Status: workflowcode.ResultSucceeded}, ReplayedOps: &cursor}
			_, err = workflowcode.Validate(journal, response, workflowcode.DefaultLimits(time.Hour, api.WorkflowRetrySpec{MaxAttempts: 3}))
			if !errors.Is(err, workflowcode.ErrNondeterministic) {
				t.Fatalf("omitted replay returned %v", err)
			}
			// Older SDKs lack the cursor, but may never omit a pending wait.
			response.ReplayedOps = nil
			if status == workflowcode.RowAwaitingEvent {
				_, err = workflowcode.Validate(journal, response, workflowcode.DefaultLimits(time.Hour, api.WorkflowRetrySpec{MaxAttempts: 3}))
				if !errors.Is(err, workflowcode.ErrNondeterministic) {
					t.Fatalf("legacy omitted wait returned %v", err)
				}
			}
		})
	}
}

func TestSDKRepeatedMaximumNameIsAccepted(t *testing.T) {
	name := strings.Repeat("a", api.WorkflowCodeOpNameMaxBytes)
	if !api.ValidWorkflowCodeOpName(name + "#2") {
		t.Fatal("SDK-generated repeat name rejected")
	}
	for _, suffix := range []string{"#1", "#02", "#1001", "#2#2"} {
		if api.ValidWorkflowCodeOpName(name + suffix) {
			t.Fatalf("accepted invalid repeat suffix %q", suffix)
		}
	}
}

func TestExhaustedRepeatedNameIsALimitFailure(t *testing.T) {
	name := strings.Repeat("a", api.WorkflowCodeOpNameMaxBytes)
	rows := make([]workflowcode.Row, api.WorkflowCodeMaxOps)
	for i := range rows {
		opName := name
		if i > 0 {
			opName = fmt.Sprintf("%s#%d", name, i+1)
		}
		rows[i] = workflowcode.Row{Name: opName, Status: workflowcode.RowSucceeded, Attempt: 1,
			Input: workflowcode.EncodeDescriptor(workflowcode.Descriptor{Seq: i, Kind: workflowcode.KindStep}), Output: json.RawMessage(`1`)}
	}
	journal, err := workflowcode.BuildJournal(rows)
	if err != nil {
		t.Fatal(err)
	}
	response := workflowcode.Response{Protocol: api.WorkflowCodeProtocol,
		Ops:    []workflowcode.ReportedOp{{Seq: api.WorkflowCodeMaxOps, Name: name + "#1001", Kind: workflowcode.KindStep, Status: workflowcode.ResultSucceeded}},
		Result: workflowcode.Result{Status: workflowcode.ResultRunning}}
	_, err = workflowcode.Validate(journal, response, workflowcode.DefaultLimits(time.Hour, api.WorkflowRetrySpec{MaxAttempts: 1}))
	if !errors.Is(err, workflowcode.ErrLimit) {
		t.Fatalf("exhausted repeat would be retried: %v", err)
	}
}

func TestCodeErrorsAreSafeForPostgres(t *testing.T) {
	journal, err := workflowcode.BuildJournal(nil)
	if err != nil {
		t.Fatal(err)
	}
	response := workflowcode.Response{Protocol: api.WorkflowCodeProtocol,
		Ops:    []workflowcode.ReportedOp{{Seq: 0, Name: "work", Kind: workflowcode.KindStep, Status: workflowcode.ResultFailed, Error: "bad\x00provider", NonRetryable: true}},
		Result: workflowcode.Result{Status: workflowcode.ResultFailed, Error: "bad\x00workflow"},
	}
	plan, err := workflowcode.Validate(journal, response, workflowcode.DefaultLimits(time.Hour, api.WorkflowRetrySpec{MaxAttempts: 1}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsRune(plan.Result.Error, 0) || strings.ContainsRune(plan.Ops[0].Error, 0) {
		t.Fatal("NUL survived protocol validation")
	}
}
