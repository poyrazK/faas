package main_test

import (
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// Prod hunt r30: `gregale app inspect shop` failed with "unexpected
// positional argument(s): shop"; it now names the command that exists.
func TestAppCommandWordSuggestsTopLevelCommand(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")
	t.Setenv("FAAS_API", "http://127.0.0.1:1")
	main.WithResetJSONOutput(t, false)
	_, restoreOut := main.CaptureStdout(t)
	stderr, restoreErr := main.CaptureStderr(t)
	code := main.Run([]string{"app", "inspect", "shop"})
	restoreErr()
	restoreOut()
	got := stderr.String()
	if code == 0 || !strings.Contains(got, "gregale inspect shop") || strings.Contains(got, "unexpected positional") {
		t.Fatalf("exit %d, advice:\n%s", code, got)
	}
}

// Prod hunt r30: a bare `gregale app restart` outside a linked project
// answered `unknown command "app restart"` although the verb exists (the CLI
// recommends it after `env set`); it now asks for the app.
func TestBareAppVerbAsksForTheApp(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")
	t.Setenv("FAAS_API", "http://127.0.0.1:1")
	t.Chdir(t.TempDir())
	main.WithResetJSONOutput(t, false)
	_, restoreOut := main.CaptureStdout(t)
	stderr, restoreErr := main.CaptureStderr(t)
	code := main.Run([]string{"app", "restart"})
	restoreErr()
	restoreOut()
	if got := stderr.String(); code == 0 || strings.Contains(got, "unknown command") || !strings.Contains(got, "app") {
		t.Fatalf("exit %d, advice:\n%s", code, got)
	}
}

// Prod hunt r30: `gregale deployment list --app shop` printed the usage for
// one release; it now points at the release list.
func TestDeploymentListPointsAtReleaseList(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")
	t.Setenv("FAAS_API", "http://127.0.0.1:1")
	main.WithResetJSONOutput(t, false)
	_, restoreOut := main.CaptureStdout(t)
	stderr, restoreErr := main.CaptureStderr(t)
	code := main.Run([]string{"deployment", "list", "--app", "shop"})
	restoreErr()
	restoreOut()
	if got := stderr.String(); code == 0 || !strings.Contains(got, "gregale release list --app shop") {
		t.Fatalf("exit %d, advice:\n%s", code, got)
	}
}
