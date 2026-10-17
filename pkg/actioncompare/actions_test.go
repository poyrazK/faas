package actioncompare

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func actionEvidence() *Evidence {
	return &Evidence{Version: Version, Status: "complete", Provenance: Provenance,
		ContractSHA256: strings.Repeat("a", 64), FixtureSHA256: strings.Repeat("b", 64),
		Snapshot: &api.ScenarioActionSnapshot{Version: api.ScenarioActionSnapshotVersion, Scope: api.ScenarioActionScope,
			RunID: strings.Repeat("a", 32), Status: "complete", SealedAt: time.Now().UTC().Format(time.RFC3339Nano),
			Workloads: []string{"primary"}, Intents: []api.ScenarioCapturedAction{}}}
}

func capturedIntent(payload string) api.ScenarioCapturedAction {
	return api.ScenarioCapturedAction{ID: uuid.NewString(), OperationID: uuid.NewString(), WebhookID: uuid.NewString(),
		Workload: "primary", Source: "operation", Name: "notify", Type: "order.fulfilled", ReceiverSHA256: strings.Repeat("c", 64), Payload: json.RawMessage(payload)}
}

func setIntents(evidence *Evidence, intents ...api.ScenarioCapturedAction) {
	evidence.Snapshot.Intents = append([]api.ScenarioCapturedAction{}, intents...)
	evidence.Snapshot.CapturedCount = int64(len(intents))
}

func TestCapturedActionsCompareBusinessIntentAndMultiplicity(t *testing.T) {
	for _, tc := range []struct {
		name, status string
		mutate       func(*Evidence)
	}{
		{"independent-runtime-identities", "passed", func(e *Evidence) {
			e.Snapshot.RunID = strings.Repeat("b", 32)
			setIntents(e, capturedIntent(`{"amount":1e0,"items":[2,3]}`))
		}},
		{"duplicate-notification-on-retry", "failed", func(e *Evidence) {
			setIntents(e, e.Snapshot.Intents[0], capturedIntent(`{"items":[2,3],"amount":1}`))
		}},
		{"payload-expands-records", "failed", func(e *Evidence) { e.Snapshot.Intents[0].Payload = json.RawMessage(`{"items":[2,3,4],"amount":1}`) }},
		{"array-order-matters", "failed", func(e *Evidence) { e.Snapshot.Intents[0].Payload = json.RawMessage(`{"items":[3,2],"amount":1}`) }},
		{"destination-changed", "failed", func(e *Evidence) { e.Snapshot.Intents[0].ReceiverSHA256 = strings.Repeat("d", 64) }},
		{"event-type-changed", "failed", func(e *Evidence) { e.Snapshot.Intents[0].Type = "order.refunded" }},
		{"missing-action", "failed", func(e *Evidence) { setIntents(e) }},
		{"renamed-action", "failed", func(e *Evidence) { e.Snapshot.Intents[0].Name = "notify-again" }},
		{"different-fixtures", "inconclusive", func(e *Evidence) { e.FixtureSHA256 = strings.Repeat("d", 64) }},
		{"different-recipe", "inconclusive", func(e *Evidence) { e.ContractSHA256 = strings.Repeat("d", 64) }},
		{"different-workloads", "inconclusive", func(e *Evidence) { e.Snapshot.Workloads = append(e.Snapshot.Workloads, "worker") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before, after := actionEvidence(), actionEvidence()
			setIntents(before, capturedIntent(`{"items":[2,3],"amount":1.00}`))
			setIntents(after, capturedIntent(`{"amount":1,"items":[2,3]}`))
			tc.mutate(after)
			result := Compare(before, after)
			if result.Status != tc.status {
				t.Fatalf("comparison=%+v, want %s", result, tc.status)
			}
			if result.Status == "inconclusive" && (result.BeforeCount != nil || result.AfterCount != nil) {
				t.Fatal("unavailable inventory reported as a measured count")
			}
			if result.Status == "failed" && len(result.Changes) == 0 {
				t.Fatal("behavior difference had no reviewable evidence")
			}
		})
	}
}

func TestCapturedActionsIgnoreCommitOrderAndPreserveWorkflowIdentity(t *testing.T) {
	before, after := actionEvidence(), actionEvidence()
	first, second := capturedIntent(`{"order":1}`), capturedIntent(`{"order":2}`)
	first.Source, first.Workflow, first.Step = "workflow", "import", "notify"
	second.Source, second.Workflow, second.Step = "workflow", "import", "notify"
	setIntents(before, first, second)
	setIntents(after, second, first)
	if result := Compare(before, after); result.Status != "passed" {
		t.Fatalf("commit order affected comparison: %+v", result)
	}
	after.Snapshot.Intents[0].Step = "cleanup"
	if result := Compare(before, after); result.Status != "failed" || len(result.Changes) != 2 {
		t.Fatalf("different workflow step ignored: %+v", result)
	}
	if result := Compare(actionEvidence(), actionEvidence()); result.Status != "passed" || *result.BeforeCount != 0 || *result.AfterCount != 0 {
		t.Fatalf("explicit empty inventory rejected: %+v", result)
	}
}

func TestCapturedActionsNeverPassIncompleteOrAmbiguousEvidence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Evidence)
	}{
		{"application-reported", func(e *Evidence) { e.Provenance = "application_reported" }},
		{"capture-version", func(e *Evidence) { e.Version++ }},
		{"wrapper-incomplete", func(e *Evidence) { e.Status = "incomplete" }},
		{"collection-error", func(e *Evidence) { e.Error = "failed" }},
		{"missing-snapshot", func(e *Evidence) { e.Snapshot = nil }},
		{"pending", func(e *Evidence) { e.Snapshot.Status, e.Snapshot.PendingManagedWork = "pending", 1 }},
		{"unsealed", func(e *Evidence) { e.Snapshot.SealedAt = "" }},
		{"truncated", func(e *Evidence) { e.Snapshot.Truncated = true }},
		{"untracked-effect", func(e *Evidence) { e.Snapshot.IncompleteManagedEffects = 1 }},
		{"count-mismatch", func(e *Evidence) { e.Snapshot.CapturedCount++ }},
		{"missing-receiver-fingerprint", func(e *Evidence) { e.Snapshot.Intents[0].ReceiverSHA256 = "" }},
		{"duplicate-json-key", func(e *Evidence) { e.Snapshot.Intents[0].Payload = json.RawMessage(`{"private":1,"private":2}`) }},
		{"invalid-json", func(e *Evidence) { e.Snapshot.Intents[0].Payload = json.RawMessage(`{"private":`) }},
		{"unknown-workload", func(e *Evidence) { e.Snapshot.Intents[0].Workload = "unknown" }},
		{"duplicate-intent-id", func(e *Evidence) { setIntents(e, e.Snapshot.Intents[0], e.Snapshot.Intents[0]) }},
		{"missing-inventory", func(e *Evidence) { e.Snapshot.Intents, e.Snapshot.CapturedCount = nil, 0 }},
		{"negative-suppression-count", func(e *Evidence) { e.Snapshot.SuppressedDeliveries = -1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before, after := actionEvidence(), actionEvidence()
			setIntents(before, capturedIntent(`{}`))
			setIntents(after, capturedIntent(`{}`))
			tc.mutate(after)
			result := Compare(before, after)
			if result.Status != "inconclusive" || result.BeforeCount != nil || strings.Contains(result.Reason, "private") {
				t.Fatalf("invalid capture passed or leaked payload: %+v", result)
			}
		})
	}
	if Compare(nil, nil) != nil || Compare(actionEvidence(), nil).Status != "inconclusive" {
		t.Fatal("legacy or one-sided evidence was misclassified")
	}
}
