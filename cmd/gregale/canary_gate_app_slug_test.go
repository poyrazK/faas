package main_test

import (
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// TestCanaryGateNamesAnAppSlug: `canary gate h14-py` reached the API with an
// app slug for a deployment ID on production-us rc.294 (Hunt 14 H14-65).
func TestCanaryGateNamesAnAppSlug(t *testing.T) {
	t.Setenv("FAAS_API", "http://127.0.0.1:9")
	t.Setenv("FAAS_TOKEN", "test-token")
	for _, args := range [][]string{{"canary", "gate", "h14-py"}, {"canary", "advance", "h14-py", "--expected-step", "1"}} {
		main.ResetJSONOut(t)
		_, stderr, restore := main.SwapIO(t)
		code := main.Run(args)
		restore()
		if code == 0 || !strings.Contains(stderr(), "takes a deployment ID, not an app; list h14-py's deployments with `gregale release list h14-py`") {
			t.Fatalf("%v: exit=%d stderr=%q", args, code, stderr())
		}
	}
}
