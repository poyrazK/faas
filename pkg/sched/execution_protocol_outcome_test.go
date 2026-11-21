package sched

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/executionproto"
)

func TestProtocolFailureNullNormalizesForTerminalReceipt(t *testing.T) {
	for _, tc := range []struct {
		name, code, wantCode string
		status               api.ExecutionStatus
		result, wantResult   string
	}{
		{"guest error", "guest_error", "guest_error", api.ExecutionStatusFailed, "null", ""},
		{"output limit", "output_limit", "output_limit_exceeded", api.ExecutionStatusFailed, "null", ""},
		{"artifact error", "artifact_invalid", "artifact_invalid", api.ExecutionStatusFailed, "null", ""},
		{"timeout", "timeout", "timeout", api.ExecutionStatusTimedOut, "null", ""},
		{"oom", "out_of_memory", "out_of_memory", api.ExecutionStatusOutOfMemory, " \nnull ", ""},
		{"successful null", "", "", api.ExecutionStatusSucceeded, "null", "null"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			outcome := normalizeExecutionOutcome(outcomeFromProtocolResult(executionproto.Result{
				Status: tc.status, Result: json.RawMessage(tc.result), FailureCode: tc.code,
				FailureMessage: "private guest detail", OutputTruncated: tc.code == "output_limit",
			}), 4096)
			if outcome.Status != tc.status || outcome.FailureCode != tc.wantCode || string(outcome.Result) != tc.wantResult {
				t.Fatalf("protocol result became an invalid receipt: %+v", outcome)
			}
			if outcome.FailureMessage == "private guest detail" || outcome.OutputTruncated != (tc.code == "output_limit") {
				t.Fatalf("failure metadata changed: %+v", outcome)
			}
		})
	}
}

func TestProtocolFailureCannotCarryApplicationResult(t *testing.T) {
	for _, result := range []string{"42", `{"private":true}`} {
		outcome := normalizeExecutionOutcome(outcomeFromProtocolResult(executionproto.Result{
			Status: api.ExecutionStatusFailed, Result: json.RawMessage(result), FailureCode: "guest_error",
		}), 4096)
		if outcome.FailureCode != "guest_protocol_error" || len(outcome.Result) != 0 {
			t.Fatalf("failed result was accepted: %+v", outcome)
		}
	}
}
