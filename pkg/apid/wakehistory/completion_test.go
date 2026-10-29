package wakehistory

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// Hunt 12 H12-49: wake.boot_started names the planned method, and only
// wake.boot_completed says a planned restore fell back to a cold boot.
func TestApplyCompletionReportsTheMethodVmmdRan(t *testing.T) {
	planned := api.WakeTimelineJSONRow{Method: "restore", Tier: "init"}
	tests := []struct {
		name string
		row  api.WakeTimelineJSONRow
		data string
		want api.WakeTimelineJSONRow
	}{
		{"no completion yet", planned, ``, planned},
		{"restore as planned", planned, `{"method":"restore"}`, planned},
		{"completion without a method", planned, `{"tier":"init"}`, planned},
		{"malformed completion", planned, `{"method":`, planned},
		{"stale snapshot fallback", planned, `{"method":"cold_boot"}`,
			api.WakeTimelineJSONRow{Method: "cold_boot", Tier: "cold_boot_fallback", PlannedMethod: "restore"}},
		{"after-restore hook fallback", planned, `{"method":"cold_boot","restore_fallback_reason":"after_restore_failed"}`,
			api.WakeTimelineJSONRow{Method: "cold_boot", Tier: "cold_boot_fallback", PlannedMethod: "restore", RestoreFallbackReason: "after_restore_failed"}},
		{"an unknown reason is not relayed", planned, `{"method":"cold_boot","restore_fallback_reason":"snapshot backing images changed"}`,
			api.WakeTimelineJSONRow{Method: "cold_boot", Tier: "cold_boot_fallback", PlannedMethod: "restore"}},
		{"planned cold boot", api.WakeTimelineJSONRow{Method: "cold_boot", Tier: "cold_boot_fallback"}, `{"method":"cold_boot"}`,
			api.WakeTimelineJSONRow{Method: "cold_boot", Tier: "cold_boot_fallback"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.row
			applyCompletion(&got, json.RawMessage(tt.data))
			if got != tt.want {
				t.Fatalf("row = %+v, want %+v", got, tt.want)
			}
		})
	}
}
