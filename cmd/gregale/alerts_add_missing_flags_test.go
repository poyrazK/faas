package main_test

import (
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// Production-us rc.294: `gregale alerts add ... --webhook-url URL` without a
// secret printed only the compacted usage line, which ends in "[flags]" and
// no longer mentions the secret, so nothing said what was wrong (Hunt 14
// H14-8).
func TestAlertsAddNamesMissingFlags(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"webhook URL without a secret",
			[]string{"add", "--app", "h14-py", "--name", "h14-synth-fail", "--metric", "error_rate", "--comparison", "gte", "--threshold", "1", "--window-spec", "5m", "--webhook-url", "https://h14-fn.gregale.dev/alert"},
			"missing: --webhook-secret or --webhook-secret-stdin (required with --webhook-url)"},
		{"no destination and no metric",
			[]string{"add", "--app", "h14-py", "--name", "h14-synth-fail", "--comparison", "gte", "--threshold", "1", "--window-spec", "5m"},
			"missing: --metric, --webhook-url with --webhook-secret, or --channel"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("FAAS_API", "http://127.0.0.1:1")
			t.Setenv("FAAS_TOKEN", "fp_test_x")
			main.ResetJSONOut(t)
			// A script: stdin is not a terminal, so nothing prompts for the secret.
			noTTY := false
			prev := *main.TestOnlyTTY
			*main.TestOnlyTTY = &noTTY
			t.Cleanup(func() { *main.TestOnlyTTY = prev })
			stderr, restore := main.CaptureStderr(t)
			code := main.CmdAlerts(tc.args)
			restore()
			if code == 0 || !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("exit %d; stderr missing %q:\n%s", code, tc.want, stderr.String())
			}
		})
	}
}
