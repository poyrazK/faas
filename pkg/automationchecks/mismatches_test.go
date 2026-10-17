// adr: 1290
package automationchecks

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMismatchFieldsAreCompleteAndContainNoValues(t *testing.T) {
	yes, count, reason := true, 2, "PRIVATE-EXPECTED-REASON"
	attempts := []api.AutomationCheckAttempt{{Outcome: "success"}}
	e := api.AutomationCheckExpectation{Step: "main", State: "mocked", Reason: &reason, WhenMatched: &yes,
		Output: json.RawMessage(`"PRIVATE-EXPECTED-OUTPUT"`), AttemptCount: &count, Attempts: &attempts}
	trace := []api.AutomationSimulationStep{{StepName: "main", State: "failed", Reason: "PRIVATE-OBSERVED-REASON", Output: json.RawMessage(`"PRIVATE-OBSERVED-OUTPUT"`)}}
	want := []string{"state", "reason", "when_matched", "output", "attempt_count", "attempts"}
	if got := MismatchFields(e, trace); !reflect.DeepEqual(got, want) {
		t.Fatalf("mismatch fields: %v", got)
	}
	for _, rows := range [][]api.AutomationSimulationStep{nil, {trace[0], trace[0]}} {
		if got := MismatchFields(e, rows); !reflect.DeepEqual(got, []string{"step"}) {
			t.Fatalf("ambiguous or missing target: %v", got)
		}
	}
}

func TestMismatchFieldsMatchLoopItemsAndExactNumbers(t *testing.T) {
	index, count := 0, 0
	e := api.AutomationCheckExpectation{Step: "batch/action", Loop: "batch", ItemIndex: &index, Output: json.RawMessage(`9007199254740993`), AttemptCount: &count}
	trace := []api.AutomationSimulationStep{{StepName: "batch[0]", ParentStep: "batch", ItemIndex: &index, Output: json.RawMessage(`9007199254740993.0`)}}
	if got := MismatchFields(e, trace); len(got) != 0 {
		t.Fatalf("loop numeric match: %v", got)
	}
	trace[0].Output = json.RawMessage(`9007199254740992`)
	if got := MismatchFields(e, trace); !reflect.DeepEqual(got, []string{"output"}) {
		t.Fatalf("rounded numeric mismatch: %v", got)
	}
	e.ItemIndex = nil
	if got := MismatchFields(e, trace); !reflect.DeepEqual(got, []string{"step"}) {
		t.Fatalf("malformed item target: %v", got)
	}
}
