package main

import (
	"strings"
	"testing"
)

// Prod hunt #4 (H4-14): `send <app> --data not-json` answered "--payload must
// be valid JSON", naming a flag send does not have. Each JSON flag must name
// itself in the validation error.
func TestJSONFlagErrorsNameTheirFlag(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func() int
		want string
	}{
		{name: "send --data", run: func() int { return cmdSend([]string{"api", "--type", "t", "--data", "not-json"}) }, want: "--data must be valid JSON"},
		{name: "deliver --data", run: func() int {
			return cmdDeliver([]string{"api", "wh_1", "--type", "t", "--data", "not-json"})
		}, want: "--data must be valid JSON"},
		{name: "events preview --data", run: func() int {
			return cmdEvents([]string{"preview", "src", "t", "--data", "not-json"})
		}, want: "--data must be valid JSON"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stderr, restore := captureStderr(t)
			code := tc.run()
			restore()
			got := stderr.String()
			if code == 0 || !strings.Contains(got, tc.want) || strings.Contains(got, "--payload") {
				t.Fatalf("exit=%d stderr=%q, want a non-zero exit naming %q and never --payload", code, got, tc.want)
			}
		})
	}
	if _, err := resolveJSONFlag("--config", "{"); err == nil || !strings.Contains(err.Error(), "--config must be valid JSON") {
		t.Fatalf("resolveJSONFlag(--config) err = %v, want it to name --config", err)
	}
}
