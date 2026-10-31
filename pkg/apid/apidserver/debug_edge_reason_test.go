package apidserver_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// A warm request's edge stage said "linked wake markers anchor impossible
// ordering" next to "request did not record a wake" (Hunt 12 H12-24).
func TestDebugEdgeReasonMentionsWakeOnlyWithAWake(t *testing.T) {
	tests := []struct {
		name   string
		wakeID string
		want   string
	}{
		{"warm request", "", "estimated from collapsed request telemetry"},
		{"woken request", "wake-1", "estimated from collapsed request telemetry; request markers are kept in order around the linked wake"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := apidserver.BuildDebugRequestCorrelation(api.DebugTelemetryRequestItem{WakeID: tt.wakeID, LatencyMS: 740, Status: 502}, nil, nil)
			if got.Stages[0].Reason != tt.want {
				t.Fatalf("edge reason = %q, want %q", got.Stages[0].Reason, tt.want)
			}
		})
	}
}
