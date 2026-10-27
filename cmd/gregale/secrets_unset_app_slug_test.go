package main_test

import (
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// TestSecretsUnsetTakesALeadingAppSlug: `secrets rm h14-py H14_SWEEP`
// answered only "no linked Gregale project context" on production-us rc.297,
// while `secrets set h14-py KEY=VALUE` takes the slug (Hunt 14 H14-86).
// Secret keys are upper-case, so a lone key is never taken for the app.
func TestSecretsUnsetTakesALeadingAppSlug(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("FAAS_API", "http://127.0.0.1:9")
	t.Setenv("FAAS_TOKEN", "test-token")
	main.ResetJSONOut(t)
	for _, verb := range []string{"rm", "unset"} {
		stdout, stderr, restore := main.SwapIO(t)
		code := main.Run([]string{"secrets", verb, "h14-py", "H14_SWEEP", "--dry-run"})
		restore()
		if code != 0 || !strings.Contains(stdout.String()+stderr(), "app=h14-py") {
			t.Fatalf("secrets %s: exit=%d out=%q err=%q", verb, code, stdout.String(), stderr())
		}
	}
	_, stderr, restore := main.SwapIO(t)
	code := main.Run([]string{"secrets", "rm", "h14-py", "H14_SWEEP", "--app", "other", "--dry-run"})
	restore()
	if code != 1 || !strings.Contains(stderr(), "pass the app once") {
		t.Fatalf("conflicting slug and --app: exit=%d stderr=%q", code, stderr())
	}
	_, stderr, restore = main.SwapIO(t)
	code = main.Run([]string{"secrets", "rm", "H14_SWEEP", "--dry-run"})
	restore()
	if code != 1 || !strings.Contains(stderr(), "no linked Gregale project context") {
		t.Fatalf("a lone key must not become the app: exit=%d stderr=%q", code, stderr())
	}
}
