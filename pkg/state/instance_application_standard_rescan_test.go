package state_test

// adr: 595, 20261031190030

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// The MemStore guard matches application_standard_runtime_inputs_match: an
// image rescan's scan mirrors are ignored, while a regressed scan's
// parked_reason and the artifact itself stay compared. The native comparison
// is unchanged.
func TestRuntimeGuardComparisonIgnoresImageRescans(t *testing.T) {
	input := func(status, hash string, parked any, rootfs string) []byte {
		raw, err := json.Marshal(map[string]any{
			"app_id": "a", "account_plan": "pro", "instance_mode": "normal", "adoptions": []any{}, "materialized_fields": []any{},
			"artifact": map[string]any{"id": "d", "scope": "production", "scan_status": status, "scan_result_hash": hash,
				"parked_reason": parked, "rootfs_key": rootfs},
		})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	captured := input("passed", "h1", nil, "k")
	for _, tc := range []struct {
		name    string
		current []byte
		want    bool
	}{
		{"unchanged", captured, true},
		{"rescanned", input("failed", "h2", nil, "k"), true},
		{"scan regressed", input("failed", "h2", "security_scan_regressed", "k"), false},
		{"artifact bytes changed", input("passed", "h1", nil, "k2"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := state.StandardRuntimeInputsMatch(captured, tc.current)
			if err != nil || got != tc.want {
				t.Fatalf("guard match = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
	if got, err := state.StandardNativeRuntimeInputsMatch(captured, input("failed", "h2", nil, "k")); err != nil || got {
		t.Fatalf("a rescan matched the native comparison: %v, %v", got, err)
	}
}
