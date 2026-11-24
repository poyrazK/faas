package main_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// Prod hunt r38: `rollback` in a linked checkout printed only its usage.
func TestRollbackDefaultsToLinkedApp(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got == "" {
			got = r.URL.Path
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"title":"not found","status":404,"code":"not_found"}`))
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	linkCheckout(t)
	_, stdout, stderr := runCLI(t, "rollback", "--no-wait")
	if !strings.Contains(got, "/apps/demo") {
		t.Fatalf("first request %q, want the linked app demo\n%s%s", got, stdout, stderr)
	}
}

// Prod hunt r39: cron-example's next steps ended at deploy; nothing scheduled it.
func TestCronExampleNextStepsSchedule(t *testing.T) {
	if steps := strings.Join(main.NextStepsFor("cron-example"), "\n"); !strings.Contains(steps, "gregale cron create") {
		t.Fatalf("cron-example next steps never schedule the handler:\n%s", steps)
	}
}

// Prod hunt r39: `trace list` answered "request telemetry not found".
func TestTraceListPointsToHTTPLogs(t *testing.T) {
	t.Setenv("FAAS_TOKEN", "test-token")
	code, _, stderr := runCLI(t, "trace", "list")
	if code == 0 || !strings.Contains(stderr, "logs --source http") {
		t.Fatalf("trace list = %d %q, want a pointer to logs --source http", code, stderr)
	}
}

// Prod hunt r39: `app binding list` in a linked checkout answered "no app
// named list".
func TestAppBindingListDefaultsToLinkedApp(t *testing.T) {
	for _, args := range [][]string{{"app", "binding", "list"}, {"app", "binding"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var got string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got == "" {
					got = r.URL.Path
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"title":"not found","status":404,"code":"not_found"}`))
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "test-token")
			linkCheckout(t)
			_, stdout, stderr := runCLI(t, args...)
			if !strings.Contains(got, "/apps/demo") {
				t.Fatalf("first request %q, want the linked app demo\n%s%s", got, stdout, stderr)
			}
		})
	}
}

// Prod hunt r40: the empty domain list told scripts to run `domain setup`,
// which refuses without a terminal.
func TestEmptyDomainListNamesScriptableVerb(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	noTTY := false
	prev := *main.TestOnlyTTY
	*main.TestOnlyTTY = &noTTY
	t.Cleanup(func() { *main.TestOnlyTTY = prev })
	code, stdout, stderr := runCLI(t, "domain", "list")
	if code != 0 || !strings.Contains(stdout, "gregale domain add <domain>") {
		t.Fatalf("domain list = %d %q %q, want the domain add hint", code, stdout, stderr)
	}
}

// Prod hunt r40: `gregale app --min 1` in a linked checkout took --min as the
// slug and refused "1".
func TestAppSettingsFlagsDefaultToLinkedApp(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch && got == "" {
			got = r.URL.Path
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"title":"not found","status":404,"code":"not_found"}`))
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	linkCheckout(t)
	_, stdout, stderr := runCLI(t, "app", "--idle", "60")
	if strings.Contains(stderr, "unexpected positional") || !strings.Contains(got, "/apps/demo") {
		t.Fatalf("app --idle 60 patched %q, want the linked app demo\n%s%s", got, stdout, stderr)
	}
}

// Prod hunt r38: bare `gregale env` listed only the clone and sync verbs,
// not set/unset/list.
func TestEnvUsageListsSecretVerbs(t *testing.T) {
	_, _, stderr := runCLI(t, "env")
	if !strings.Contains(stderr, "set|unset|list") {
		t.Fatalf("env usage %q lacks set|unset|list", stderr)
	}
}
