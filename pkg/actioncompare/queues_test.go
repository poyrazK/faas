// adr: 20261021090000
package actioncompare

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func queueEvidence(messages ...api.ScenarioCapturedQueueMessage) *Evidence {
	e := actionEvidence()
	e.Snapshot.QueueCaptureVersion = api.ScenarioQueueCaptureVersion
	e.Snapshot.QueueMessages = messages
	return e
}

func queueMessage(content string) api.ScenarioCapturedQueueMessage {
	return api.ScenarioCapturedQueueMessage{ID: uuid.NewString(), Workload: "primary", Kind: api.ScenarioQueueKindQueue, Target: "workload:worker", QueueName: "imports", Message: json.RawMessage(content)}
}

func delayedTask(schedule, content string) api.ScenarioCapturedQueueMessage {
	return api.ScenarioCapturedQueueMessage{ID: uuid.NewString(), Workload: "primary", Kind: api.ScenarioQueueKindDelayedTask, Target: "app:billing", Schedule: schedule, Message: json.RawMessage(content)}
}

func TestCapturedQueueMessagesCompareContentScheduleAndMultiplicity(t *testing.T) {
	for _, tc := range []struct {
		name, status string
		mutate       func(*Evidence)
	}{
		{"independent-ids-and-equal-json", "passed", func(*Evidence) {}},
		{"duplicate-send", "failed", func(e *Evidence) {
			e.Snapshot.QueueMessages = append(e.Snapshot.QueueMessages, queueMessage(`{"payload":{"order":1}}`))
		}},
		{"missing-send", "failed", func(e *Evidence) { e.Snapshot.QueueMessages = e.Snapshot.QueueMessages[1:] }},
		{"content-changed", "failed", func(e *Evidence) { e.Snapshot.QueueMessages[0].Message = json.RawMessage(`{"payload":{"order":2}}`) }},
		{"queue-changed", "failed", func(e *Evidence) { e.Snapshot.QueueMessages[0].QueueName = "exports" }},
		{"target-changed", "failed", func(e *Evidence) { e.Snapshot.QueueMessages[0].Target = "app:worker" }},
		{"delay-changed", "failed", func(e *Evidence) { e.Snapshot.QueueMessages[1].Schedule = "delay:61" }},
		{"absolute-instead-of-delay", "failed", func(e *Evidence) { e.Snapshot.QueueMessages[1].Schedule = "absolute" }},
		{"older-server-without-queue-capture", "inconclusive", func(e *Evidence) {
			e.Snapshot.QueueCaptureVersion, e.Snapshot.QueueMessages = 0, nil
		}},
		{"incomplete-queue-capture", "inconclusive", func(e *Evidence) { e.Snapshot.IncompleteQueueMessages = 1 }},
		{"queue-evidence-without-version", "inconclusive", func(e *Evidence) { e.Snapshot.QueueCaptureVersion = 0 }},
		{"message-for-unknown-workload", "inconclusive", func(e *Evidence) { e.Snapshot.QueueMessages[0].Workload = "stranger" }},
		{"queue-send-with-schedule", "inconclusive", func(e *Evidence) { e.Snapshot.QueueMessages[0].Schedule = "delay:1" }},
		{"delayed-task-without-schedule", "inconclusive", func(e *Evidence) { e.Snapshot.QueueMessages[1].Schedule = "" }},
		{"unnormalized-target", "inconclusive", func(e *Evidence) { e.Snapshot.QueueMessages[0].Target = "worker" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := queueEvidence(queueMessage(`{"payload":{"order":1.0}}`), delayedTask("delay:60", `{"payload":{"invoice":7}}`))
			after := queueEvidence(queueMessage(`{"payload":{"order":1}}`), delayedTask("delay:60", `{"payload":{"invoice":7}}`))
			tc.mutate(after)
			result := Compare(before, after)
			if result.Status != tc.status {
				t.Fatalf("comparison=%+v, want %s", result, tc.status)
			}
			if result.Status == "failed" && len(result.Changes) == 0 {
				t.Fatal("queue difference had no reviewable evidence")
			}
		})
	}
}

// A same-run delivery compares by its result and the state its handler
// reached; mismatched delivery versions are inconclusive (ADR-20261023111825).
func TestDeliveredQueueMessagesCompareResultAndState(t *testing.T) {
	delivered := func(state string) api.ScenarioCapturedQueueMessage {
		m := queueMessage(`{"payload":{"order":1}}`)
		m.QueueName, m.Delivery, m.DeliveryState = "", api.ScenarioQueueDelivered, state
		return m
	}
	evidence := func(messages ...api.ScenarioCapturedQueueMessage) *Evidence {
		e := queueEvidence(messages...)
		e.Snapshot.QueueDeliveryVersion = api.ScenarioQueueDeliveryVersion
		return e
	}
	for _, tc := range []struct {
		name, status string
		mutate       func(*Evidence)
	}{
		{"same-state", "passed", func(*Evidence) {}},
		{"handler-failed", "failed", func(e *Evidence) { e.Snapshot.QueueMessages[0].DeliveryState = "dead_letter" }},
		{"not-delivered", "failed", func(e *Evidence) {
			e.Snapshot.QueueMessages[0].Delivery, e.Snapshot.QueueMessages[0].DeliveryState = api.ScenarioQueueUnsupported, ""
		}},
		{"delivery-version-differs", "inconclusive", func(e *Evidence) {
			e.Snapshot.QueueDeliveryVersion = 0
			e.Snapshot.QueueMessages[0].Delivery, e.Snapshot.QueueMessages[0].DeliveryState = "", ""
		}},
		{"state-without-delivery", "inconclusive", func(e *Evidence) { e.Snapshot.QueueMessages[0].Delivery = "" }},
		{"unknown-result", "inconclusive", func(e *Evidence) { e.Snapshot.QueueMessages[0].Delivery = "lost" }},
		{"delivered-delayed-task", "inconclusive", func(e *Evidence) {
			e.Snapshot.QueueMessages[0].Kind, e.Snapshot.QueueMessages[0].Schedule = api.ScenarioQueueKindDelayedTask, "delay:60"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before, after := evidence(delivered("succeeded")), evidence(delivered("succeeded"))
			tc.mutate(after)
			if result := Compare(before, after); result.Status != tc.status {
				t.Fatalf("comparison=%+v, want %s", result, tc.status)
			}
		})
	}
}
