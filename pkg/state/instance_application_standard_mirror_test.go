package state

// adr: 595

import (
	"encoding/json"
	"testing"
)

func TestUnmanagedMirrorInputCompatibilityRetainsNativeFences(t *testing.T) {
	for _, tc := range []struct {
		name, mode string
		revision   int
		want       bool
	}{
		{"unmanaged mirror", "mirror", 0, true},
		{"worker", "worker", 0, false},
		{"managed mirror", "mirror", 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := map[string]any{"adoptions": []any{}, "materialized_fields": []any{}, "persisted_revision": tc.revision, "account_plan": "pro", "instance_mode": "normal"}
			captured, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			input["instance_mode"] = tc.mode
			current, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			got, err := standardRuntimeInputsMatch(captured, current)
			if err != nil || got != tc.want {
				t.Fatalf("ordinary match=%v want=%v err=%v", got, tc.want, err)
			}
			if got, err := standardNativeRuntimeInputsMatch(captured, current); err != nil || got {
				t.Fatalf("changed mode gained native receipt compatibility: %v, %v", got, err)
			}
		})
	}
}
