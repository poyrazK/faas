// adr: 20261021150000
package actioncompare

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func platformEvidence(calls ...api.ScenarioCapturedPlatformCall) *Evidence {
	e := actionEvidence()
	e.Snapshot.PlatformCaptureVersion = api.ScenarioPlatformCaptureVersion
	e.Snapshot.PlatformCalls = calls
	return e
}

func platformCall(kind, target, name, message string) api.ScenarioCapturedPlatformCall {
	return api.ScenarioCapturedPlatformCall{ID: uuid.NewString(), Workload: "primary", Kind: kind, Target: target, Name: name, Message: json.RawMessage(message)}
}

func TestCapturedPlatformCallsCompareTargetNameAndMessage(t *testing.T) {
	start := func(input string) api.ScenarioCapturedPlatformCall {
		return platformCall(api.ScenarioPlatformKindWorkflowRun, "workload:billing", "invoice", `{"input":`+input+`}`)
	}
	signal := func(payload string) api.ScenarioCapturedPlatformCall {
		return platformCall(api.ScenarioPlatformKindWorkflowEvent, "run:primary/workload:billing/invoice#1", "approved", `{"payload":`+payload+`}`)
	}
	for _, tc := range []struct {
		name, status string
		mutate       func(*Evidence)
	}{
		{"independent-ids-and-equal-json", "passed", func(*Evidence) {}},
		{"duplicate-start", "failed", func(e *Evidence) {
			e.Snapshot.PlatformCalls = append(e.Snapshot.PlatformCalls, start(`{"order":1}`))
		}},
		{"missing-signal", "failed", func(e *Evidence) { e.Snapshot.PlatformCalls = e.Snapshot.PlatformCalls[:1] }},
		{"input-changed", "failed", func(e *Evidence) { e.Snapshot.PlatformCalls[0].Message = json.RawMessage(`{"input":{"order":2}}`) }},
		{"workflow-changed", "failed", func(e *Evidence) { e.Snapshot.PlatformCalls[0].Name = "refund" }},
		{"signal-to-second-run", "failed", func(e *Evidence) {
			e.Snapshot.PlatformCalls[1].Target = "run:primary/workload:billing/invoice#2"
		}},
		{"older-server-without-platform-capture", "inconclusive", func(e *Evidence) {
			e.Snapshot.PlatformCaptureVersion, e.Snapshot.PlatformCalls = 0, nil
		}},
		{"incomplete-platform-capture", "inconclusive", func(e *Evidence) { e.Snapshot.IncompletePlatformCalls = 1 }},
		{"platform-evidence-without-version", "inconclusive", func(e *Evidence) { e.Snapshot.PlatformCaptureVersion = 0 }},
		{"unknown-kind", "inconclusive", func(e *Evidence) { e.Snapshot.PlatformCalls[0].Kind = "workflow_cancel" }},
		{"unnormalized-target", "inconclusive", func(e *Evidence) { e.Snapshot.PlatformCalls[0].Target = "billing" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := platformEvidence(start(`{"order":1.0}`), signal(`{"by":"ops"}`))
			after := platformEvidence(start(`{"order":1}`), signal(`{"by":"ops"}`))
			tc.mutate(after)
			result := Compare(before, after)
			if result.Status != tc.status {
				t.Fatalf("comparison=%+v, want %s", result, tc.status)
			}
			if result.Status == "failed" && len(result.Changes) == 0 {
				t.Fatal("platform call difference had no reviewable evidence")
			}
		})
	}
}
