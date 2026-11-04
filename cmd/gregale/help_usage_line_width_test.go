package main_test

import (
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// Production-us rc.294: `gregale events --help` put ~120 verbs on one
// 2,400-column usage line; the Commands section lists them anyway (Hunt 14
// H14-48).
func TestHelpUsageLineStaysReadableForLargeCommandFamilies(t *testing.T) {
	for _, command := range []string{"events", "automations", "jobs"} {
		t.Run(command, func(t *testing.T) {
			out, restoreOut := main.CaptureStdout(t)
			errOut, restoreErr := main.CaptureStderr(t)
			main.Run([]string{command, "--help"})
			restoreErr()
			restoreOut()
			help := out.String() + errOut.String()
			usage := ""
			if _, rest, ok := strings.Cut(help, "Usage:\n"); ok {
				usage, _, _ = strings.Cut(rest, "\n")
			}
			if usage == "" || len(usage) > 160 || !strings.Contains(help, "\nCommands:\n") {
				t.Fatalf("usage line (%d columns) %q; want ≤160 with the verbs under Commands:\n%.600s", len(usage), usage, help)
			}
		})
	}
}
