package api

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestWorkflowInputBoundedExpansionAndRuntimeParity(t *testing.T) {
	input := json.RawMessage(`{"n":9007199254740993,"value":"{{input.secret}}","null":null}`)
	for _, template := range []json.RawMessage{nil, json.RawMessage(`"{{input}}"`), json.RawMessage(`{"n":"{{input.n}}","v":"prefix-{{input.value}}","null":"{{input.null}}"}`)} {
		want, err := ResolveWorkflowStepInput(template, input, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		got, err := ResolveWorkflowStepInputBounded(template, input, nil, nil, int64(len(want)))
		if err != nil || string(got) != string(want) {
			t.Fatalf("bounded mapping differs: %s %s %v", want, got, err)
		}
		if _, err := ResolveWorkflowStepInputBounded(template, input, nil, nil, int64(len(want)-1)); !errors.Is(err, ErrWorkflowInputLimit) {
			t.Fatalf("limit not exact: %v", err)
		}
	}
	large, _ := json.Marshal(strings.Repeat("x", 1<<16))
	for _, template := range []string{`["{{input}}","{{input}}"]`, `"prefix-{{input}}{{input}}"`, `{"a":"{{input}}","b":"{{input}}"}`, `"{{input}}"`} {
		if _, err := ResolveWorkflowStepInputBounded(json.RawMessage(template), large, nil, nil, 1<<16); !errors.Is(err, ErrWorkflowInputLimit) {
			t.Fatalf("large expansion accepted: %v", err)
		}
	}
	// Bound the sum while resolving, before allocating every interpolated value.
	if _, err := ResolveWorkflowStepInputBounded(json.RawMessage(`[`+strings.Repeat(`"x{{input}}",`, 4095)+`"x{{input}}"]`), large, nil, nil, 1<<17); !errors.Is(err, ErrWorkflowInputLimit) {
		t.Fatal(err)
	}
}
