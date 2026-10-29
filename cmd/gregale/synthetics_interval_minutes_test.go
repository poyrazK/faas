package main_test

import (
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// TestSyntheticsCreateNamesTheMinutesFlag: `synthetics create
// --every-minutes 1` answered "interval_seconds must be one of [300 900
// 3600]" on production-us rc.294, naming neither the flag nor minutes (Hunt
// 14 H14-64).
func TestSyntheticsCreateNamesTheMinutesFlag(t *testing.T) {
	t.Setenv("FAAS_API", "http://127.0.0.1:9")
	t.Setenv("FAAS_TOKEN", "test-token")
	main.ResetJSONOut(t)
	_, stderr, restore := main.SwapIO(t)
	code := main.Run([]string{"synthetics", "create", "--app", "h14-fn", "--name", "probe", "--every-minutes", "1"})
	restore()
	if code == 0 || !strings.Contains(stderr(), "--every-minutes must be one of 5, 15, 60") || strings.Contains(stderr(), "interval_seconds") {
		t.Fatalf("exit=%d stderr=%q", code, stderr())
	}
}
