// adr: 20261022082721
package actioncompare

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestCapturedAsyncInvokesCompareLikeQueueWork(t *testing.T) {
	invoke := func(path string) api.ScenarioCapturedQueueMessage {
		return api.ScenarioCapturedQueueMessage{ID: uuid.NewString(), Workload: "primary", Kind: api.ScenarioQueueKindAsyncInvoke, Target: "workload:worker",
			Message: json.RawMessage(`{"invoke":{"method":"POST","path":"` + path + `"},"deadline":""}`)}
	}
	for _, tc := range []struct {
		name, status string
		mutate       func(*Evidence)
	}{
		{"same-invoke", "passed", func(*Evidence) {}},
		{"other-path", "failed", func(e *Evidence) { e.Snapshot.QueueMessages[0] = invoke("/other") }},
		{"extra-invoke", "failed", func(e *Evidence) { e.Snapshot.QueueMessages = append(e.Snapshot.QueueMessages, invoke("/sync")) }},
		{"invoke-with-schedule", "inconclusive", func(e *Evidence) { e.Snapshot.QueueMessages[0].Schedule = "delay:1" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before, after := queueEvidence(invoke("/sync")), queueEvidence(invoke("/sync"))
			tc.mutate(after)
			if result := Compare(before, after); result.Status != tc.status {
				t.Fatalf("comparison=%+v, want %s", result, tc.status)
			}
		})
	}
}
