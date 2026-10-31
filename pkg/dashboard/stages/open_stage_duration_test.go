package stages

import (
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestOpenStageDurationStopsAtTerminal is issue #2696: an open stage on a
// finished deployment must not keep counting against the clock.
func TestOpenStageDurationStopsAtTerminal(t *testing.T) {
	started := time.Now().Add(-3 * time.Hour)
	terminal := started.Add(90 * time.Second)
	for name, tc := range map[string]struct {
		status     state.DeploymentStatus
		terminalAt time.Time
		wantMs     int64
	}{
		"superseded stops at terminal_at": {status: state.DeploySuperseded, terminalAt: terminal, wantMs: 90_000},
		"failed stops at terminal_at":     {status: state.DeployFailed, terminalAt: terminal, wantMs: 90_000},
		"cancelled without terminal_at":   {status: state.DeployCancelled, wantMs: 0},
	} {
		t.Run(name, func(t *testing.T) {
			if got := openStageDurationMs(&started, string(tc.status), tc.terminalAt); got != tc.wantMs {
				t.Fatalf("openStageDurationMs = %d, want %d", got, tc.wantMs)
			}
		})
	}
	if got := openStageDurationMs(&started, string(state.DeployBuilding), time.Time{}); got < (3 * time.Hour).Milliseconds() {
		t.Fatalf("building deployment's open stage = %d ms, want it to keep counting", got)
	}

	var text strings.Builder
	ss := state.StageState{Current: state.StageImageBuild, CurrentStartedAt: &started}
	if err := RenderSummaryText(&text, ss, string(state.DeploySuperseded), terminal); err != nil {
		t.Fatalf("RenderSummaryText: %v", err)
	}
	if !strings.Contains(text.String(), "Total: 90.0s") {
		t.Fatalf("superseded summary total should stop at terminal_at (90.0s):\n%s", text.String())
	}
}
