package sched

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCrashCaptureRetentionByTrigger(t *testing.T) {
	if got := crashCaptureRetention(state.CrashTriggerLiveFork); got != api.LiveForkCaptureRetention || got <= api.AppForkMaxTTL {
		t.Fatalf("live fork retention = %v, want %v (past the fork maximum)", got, api.LiveForkCaptureRetention)
	}
	for _, trigger := range []string{state.CrashTriggerHTTP5xx, state.CrashTriggerManual, state.CrashTriggerSDK} {
		if got := crashCaptureRetention(trigger); got != api.CrashCaptureRetention {
			t.Fatalf("%s retention = %v, want %v", trigger, got, api.CrashCaptureRetention)
		}
	}
}
