package main_test

import (
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// Prod hunt r29: `gregale app deploy-token revoke TOKEN --app APP` listed the
// verbs and asked whether "revoke" was an app; it now names the verb that
// revokes a token.
func TestDeployTokenRevokeSuggestsDelete(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")
	t.Setenv("FAAS_API", "http://127.0.0.1:1")
	main.WithResetJSONOutput(t, false)
	_, restoreOut := main.CaptureStdout(t)
	stderr, restoreErr := main.CaptureStderr(t)
	main.Run([]string{"app", "deploy-token", "revoke", "tok", "--app", "shop"})
	restoreErr()
	restoreOut()
	got := stderr.String()
	if strings.Contains(got, "If revoke is an app") || !strings.Contains(got, "Did you mean") {
		t.Fatalf("revoke advice:\n%s", got)
	}
}
