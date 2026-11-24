package main_test

import (
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// Prod hunts r25/r27: `invoke APP --data '{…}'` (curl's spelling) answered
// a bare "unknown flag --data"; it now points at --payload.
func TestInvokeDataFlagSuggestsPayload(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")
	t.Setenv("FAAS_API", "http://127.0.0.1:1")
	main.WithResetJSONOutput(t, false)
	for _, flag := range []string{"--data", "--body", "--data={}"} {
		_, restoreOut := main.CaptureStdout(t)
		stderr, restoreErr := main.CaptureStderr(t)
		main.Run([]string{"invoke", "bh30-fn", flag, "{}"})
		restoreErr()
		restoreOut()
		if !strings.Contains(stderr.String(), "Did you mean --payload?") {
			t.Fatalf("%s: stderr:\n%s", flag, stderr.String())
		}
	}
}
