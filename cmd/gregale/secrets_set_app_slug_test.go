package main_test

import (
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// TestSecretsSetTakesALeadingAppSlug: `secrets set h14-py FOO=bar` answered
// only "no linked Gregale project context" on production-us rc.294 while
// `secrets list h14-py` takes the slug (Hunt 14 H14-64). A word without "="
// is no KEY=VALUE pair.
func TestSecretsSetTakesALeadingAppSlug(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("FAAS_API", "http://127.0.0.1:9")
	t.Setenv("FAAS_TOKEN", "test-token")
	main.ResetJSONOut(t)
	stdout, stderr, restore := main.SwapIO(t)
	code := main.Run([]string{"secrets", "set", "h14-py", "FOO=bar", "--dry-run"})
	restore()
	if code != 0 || !strings.Contains(stdout.String()+stderr(), "app=h14-py") {
		t.Fatalf("exit=%d out=%q err=%q", code, stdout.String(), stderr())
	}
	_, stderr, restore = main.SwapIO(t)
	code = main.Run([]string{"secrets", "set", "h14-py", "FOO=bar", "--app", "other", "--dry-run"})
	restore()
	if code != 1 || !strings.Contains(stderr(), "pass the app once") {
		t.Fatalf("conflicting slug and --app: exit=%d stderr=%q", code, stderr())
	}
}
