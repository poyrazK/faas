package main_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// unknownVerbTakesAppSlug lists the commands whose first positional may be
// an app slug or a release, so an unknown word there is looked up rather than
// refused as a verb.
var unknownVerbTakesAppSlug = map[string]bool{
	"bindings": true, "deployment": true, "deployments": true, "logs": true, "rollback": true, "workers": true,
}

// TestEveryCommandNamesItsVerbsForAnUnknownSubcommand sweeps the customer
// command manifest. On production-us rc.294, 22 commands (`approvals list`,
// `operations list`, `freeze bogus`, `jobs bogus`, …) refused an unknown verb
// without naming one they accept (Hunt 14 H14-64).
func TestEveryCommandNamesItsVerbsForAnUnknownSubcommand(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"title":"Not found","status":404,"code":"not_found"}`))
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	for _, command := range main.CustomerCliCommands() {
		if len(command.Subcommands) == 0 || command.SubcommandsAfterPositionals || unknownVerbTakesAppSlug[command.Name] {
			continue
		}
		t.Run(command.Name, func(t *testing.T) {
			main.ResetJSONOut(t)
			stdout, stderr, restore := main.SwapIO(t)
			code := main.Run([]string{command.Name, "bogusverb"})
			restore()
			out := stdout.String() + stderr()
			if code == 0 {
				t.Fatalf("exit 0 for an unknown verb:\n%s", out)
			}
			var missing []string
			for _, sub := range command.Subcommands {
				if !strings.Contains(out, sub.Name) {
					missing = append(missing, sub.Name)
				}
			}
			if len(missing) > 0 {
				t.Errorf("reply names none of %v:\n%s", missing, out)
			}
		})
	}
}

// TestSourceDirectoryPositionalPointsAtPath: `deploy ./my-app` answered only
// "accepts flags only" on production-us rc.294 (Hunt 14 H14-64).
func TestSourceDirectoryPositionalPointsAtPath(t *testing.T) {
	dir := t.TempDir()
	for _, command := range []string{"deploy", "dev"} {
		t.Run(command, func(t *testing.T) {
			main.ResetJSONOut(t)
			_, stderr, restore := main.SwapIO(t)
			code := main.Run([]string{command, dir})
			restore()
			want := "Did you mean `gregale " + command + " --path " + dir + "`?"
			if code == 0 || !strings.Contains(stderr(), want) {
				t.Fatalf("exit=%d, stderr lacks %q:\n%s", code, want, stderr())
			}
		})
	}
}

// TestCacheListPointsAtEdgeRules: cache rules are edge rules, and `cache
// list` named neither on production-us rc.294 (Hunt 14 H14-64).
func TestCacheListPointsAtEdgeRules(t *testing.T) {
	main.ResetJSONOut(t)
	_, stderr, restore := main.SwapIO(t)
	code := main.Run([]string{"cache", "list", "h14-py"})
	restore()
	for _, want := range []string{`unknown cache subcommand "list"`, "gregale edge-rules list <slug>", "Subcommands: GET, HEAD, purge"} {
		if code != 1 || !strings.Contains(stderr(), want) {
			t.Errorf("exit=%d, stderr lacks %q:\n%s", code, want, stderr())
		}
	}
}

// TestFreezeStatusNamesTheMissingApp: outside a linked checkout `freeze
// status` printed bare usage on production-us rc.294 (Hunt 14 H14-64).
func TestFreezeStatusNamesTheMissingApp(t *testing.T) {
	t.Chdir(t.TempDir())
	main.ResetJSONOut(t)
	_, stderr, restore := main.SwapIO(t)
	code := main.Run([]string{"freeze", "status"})
	restore()
	if code == 0 || !strings.Contains(stderr(), "pass --app <slug>, or link this checkout") {
		t.Fatalf("exit=%d stderr=%q", code, stderr())
	}
}

// TestWorkersListTakesAppFlag: `workers list --app` answered "unknown flag"
// on production-us rc.294 while other list verbs take it (Hunt 14 H14-64).
func TestWorkersListTakesAppFlag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"slug":"shop"},{"slug":"other"}]`))
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	main.ResetJSONOut(t)
	_, stderr, restore := main.SwapIO(t)
	code := main.Run([]string{"workers", "list", "--app", "shop"})
	restore()
	if code == 0 || !strings.Contains(stderr(), `app "shop" has execution_mode`) {
		t.Fatalf("--app did not narrow the list to shop: exit=%d stderr=%q", code, stderr())
	}
	_, stderr, restore = main.SwapIO(t)
	code = main.Run([]string{"workers", "list", "shop", "--app", "other"})
	restore()
	if code != 1 || !strings.Contains(stderr(), "usage: gregale workers [list]") {
		t.Fatalf("conflicting slug and --app: exit=%d stderr=%q", code, stderr())
	}
}

// TestLogsNamesTheFlagThatChoseHTTPLogs: `logs h14-py --grep healthz --limit
// 3` answered "HTTP log queries cannot be combined with --grep" on
// production-us rc.294 though it never asked for HTTP logs (Hunt 14 H14-64).
func TestLogsNamesTheFlagThatChoseHTTPLogs(t *testing.T) {
	t.Setenv("FAAS_API", "http://127.0.0.1:9")
	t.Setenv("FAAS_TOKEN", "test-token")
	main.ResetJSONOut(t)
	_, stderr, restore := main.SwapIO(t)
	code := main.Run([]string{"logs", "h14-py", "--grep", "healthz", "--limit", "3"})
	restore()
	if code != 2 || !strings.Contains(stderr(), "--limit applies only to HTTP request logs") || !strings.Contains(stderr(), "drop --limit to read runtime logs") {
		t.Fatalf("exit=%d stderr=%q", code, stderr())
	}
	_, stderr, restore = main.SwapIO(t)
	code = main.Run([]string{"logs", "h14-py", "--source", "http", "--grep", "healthz"})
	restore()
	if code != 2 || !strings.Contains(stderr(), "HTTP log queries cannot be combined") {
		t.Fatalf("explicit --source http: exit=%d stderr=%q", code, stderr())
	}
}
