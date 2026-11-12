package apidserver_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/debugger"
)

// adr: 127 — amendment 2026-11-05: a request's evidence logs cover it from
// its start; its row is stamped when it finished.
func TestDebugEvidenceLogWindowCoversTheWholeRequest(t *testing.T) {
	finished := time.Date(2026, 11, 5, 12, 10, 0, 0, time.UTC)
	tests := []struct {
		name      string
		latencyMS int
	}{
		{"short request", 40},
		{"ten-minute stream", int((10 * time.Minute).Milliseconds())},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			since, until, ok := apidserver.DebugEvidenceLogWindow(api.DebugTelemetryRequestItem{
				ReceivedAt: finished.Format(time.RFC3339Nano), LatencyMS: tt.latencyMS,
			})
			started := finished.Add(-time.Duration(tt.latencyMS) * time.Millisecond)
			if !ok || since.After(started.Add(-debugger.EvidenceLogWindowBefore)) || !until.Equal(finished.Add(debugger.EvidenceLogWindowAfter)) {
				t.Fatalf("window [%s, %s) ok=%v, want it to open %s before the start %s", since, until, ok, debugger.EvidenceLogWindowBefore, started)
			}
		})
	}
	if _, _, ok := apidserver.DebugEvidenceLogWindow(api.DebugTelemetryRequestItem{ReceivedAt: "not a time"}); ok {
		t.Fatal("an unparsable time read a window")
	}
}
