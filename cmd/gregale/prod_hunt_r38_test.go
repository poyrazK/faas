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

// Prod hunt r42: adding an already allowed egress port printed nothing.
func TestEgressPortsAddAlreadyAllowedSaysSo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"a","slug":"demo","egress_ports":[5432]}`))
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	code, stdout, stderr := runCLI(t, "app", "demo", "egress-ports", "add", "5432")
	if code != 0 || !strings.Contains(stdout, "already allows TCP 5432") {
		t.Fatalf("egress-ports add = %d %q %q, want an already-allowed note", code, stdout, stderr)
	}
}

// Prod hunt r43: a feature the installation does not run (501) exited 3,
// temporary_failure, so agents retried what could never succeed.
func TestNotImplementedIsNotTemporary(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = w.Write([]byte(`{"title":"Image runs unavailable","status":501,"code":"not_implemented","detail":"image runs are not enabled on this Gregale installation"}`))
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	code, _, stderr := runCLI(t, "--json", "run", "--image", "alpine", "true")
	if code != 1 || strings.Contains(stderr, "temporary_failure") {
		t.Fatalf("501 = exit %d %q, want exit 1 and no temporary_failure", code, stderr)
	}
}

// Prod hunt r43: `flag list` printed "needs --project and --project" and
// ignored the linked project.
func TestFlagListUsesLinkedProject(t *testing.T) {
	t.Setenv("FAAS_TOKEN", "test-token")
	t.Chdir(t.TempDir())
	if _, _, stderr := runCLI(t, "flag", "list"); strings.Contains(stderr, "--project and --project") {
		t.Fatalf("flag list usage repeats --project: %q", stderr)
	}
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
	linkCheckout(t)
	_, stdout, stderr := runCLI(t, "flag", "list")
	if !strings.Contains(got, "/demo") {
		t.Fatalf("flag list requested %q, want the linked project demo\n%s%s", got, stdout, stderr)
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
