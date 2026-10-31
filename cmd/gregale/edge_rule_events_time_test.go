package main_test

import (
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// Edge-rule event rows print UTC and say so; they were the viewer's local
// time with no zone, next to a "since … UTC" line (Hunt 12).
func TestPrintEdgeRuleEventsUsesUTC(t *testing.T) {
	east := time.FixedZone("UTC+3", 3*3600)
	tests := []struct {
		name string
		at   time.Time
		want string
	}{
		{"UTC instant", time.Date(2026, 10, 22, 0, 4, 36, 0, time.UTC), "2026-10-22 00:04:36"},
		{"offset instant", time.Date(2026, 10, 22, 3, 4, 36, 0, east), "2026-10-22 00:04:36"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout, restore := main.CaptureStdout(t)
			defer restore()
			main.PrintEdgeRuleEvents("web", api.EdgeRuleEventsResponse{Events: []api.EdgeRuleEventResponse{{OccurredAt: tt.at, Outcome: "matched", RuleName: "r", Method: "GET"}}})
			out := stdout.String()
			if !strings.Contains(out, "TIME (UTC)") || !strings.Contains(out, tt.want) {
				t.Fatalf("output %q lacks the UTC header or %q", out, tt.want)
			}
		})
	}
}
