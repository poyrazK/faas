package api

import (
	"strings"
	"testing"
)

// adr: 1151 — the pause an app declares: its defaults by app type, and the
// values a manifest accepts for each execution mode.
func TestEffectivePauseDefaultsByAppType(t *testing.T) {
	for _, tc := range []struct {
		pause    string
		function bool
		want     string
	}{
		{function: true, want: PauseBetweenRequests},
		{function: false, want: PauseWhenIdle},
		{pause: PauseNever, function: true, want: PauseNever},
		{pause: PauseBetweenRequests, function: false, want: PauseBetweenRequests},
	} {
		if got := EffectivePause(tc.pause, tc.function); got != tc.want {
			t.Errorf("EffectivePause(%q, function=%v) = %q, want %q", tc.pause, tc.function, got, tc.want)
		}
	}
}

func TestManifestValidatesPause(t *testing.T) {
	for _, tc := range []struct {
		name    string
		pause   string
		mode    string
		wantErr string
	}{
		{name: "default", pause: ""},
		{name: "when_idle", pause: PauseWhenIdle},
		{name: "between_requests", pause: PauseBetweenRequests},
		{name: "never on a worker", pause: PauseNever, mode: ExecutionModeWorker},
		{name: "unknown", pause: "sometimes", wantErr: "must be one of"},
		{name: "when_idle on a service", pause: PauseWhenIdle, mode: ExecutionModeService, wantErr: "requires request execution mode"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := AppManifest{Entrypoint: []string{"/app"}, Pause: tc.pause, ExecutionMode: tc.mode}
			err := m.ValidatePlan(PlanPro)
			if tc.wantErr == "" && err != nil {
				t.Fatalf("ValidatePlan = %v, want ok", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("ValidatePlan = %v, want %q", err, tc.wantErr)
			}
		})
	}
}
