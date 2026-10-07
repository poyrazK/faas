package main

import (
	"strings"
	"testing"
	"time"
)

// production-us hunt #4 (H4-28): every rollback flag mistake printed the same
// five-rule sentence. Each mistake must name its own rule.
func TestValidateRollbackFlagsNamesTheBrokenRule(t *testing.T) {
	const tm, iv = 10 * time.Minute, 2 * time.Second
	for _, tc := range []struct {
		name              string
		checked           bool
		to, current, why  string
		wait              bool
		timeout, interval time.Duration
		want              string
	}{
		{name: "plain rollback", timeout: tm, interval: iv},
		{name: "explicit target", to: "v6", timeout: tm, interval: iv},
		{name: "checked rollback", checked: true, to: "v6", current: "v8", why: "bad release", wait: true, timeout: tm, interval: iv},
		{name: "reason and wait without checked form", why: "x", wait: true, timeout: tm, interval: iv, want: "--reason and --wait only apply to a checked rollback"},
		{name: "wait alone", wait: true, timeout: tm, interval: iv, want: "--wait only applies to a checked rollback"},
		{name: "empty expected-current", checked: true, to: "v6", timeout: tm, interval: iv, want: "--expected-current requires a deployment id"},
		{name: "checked without target", checked: true, current: "v8", timeout: tm, interval: iv, want: "also needs --to"},
		{name: "zero timeout", checked: true, to: "v6", current: "v8", interval: iv, want: "--timeout must be positive"},
		{name: "zero poll interval", checked: true, to: "v6", current: "v8", timeout: tm, want: "--poll-interval must be positive"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRollbackFlags(tc.checked, tc.to, tc.current, tc.why, tc.wait, tc.timeout, tc.interval)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}
