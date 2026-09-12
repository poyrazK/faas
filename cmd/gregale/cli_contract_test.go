package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSplitArgsForFlags_PreservesDocumentedPositionalFirstForms(t *testing.T) {
	flags, pos := splitArgsForFlags([]string{"demo", "--payload", "{}", "--method", "POST", "--async"}, "async")
	if got, want := strings.Join(pos, " "), "demo"; got != want {
		t.Fatalf("positionals = %q, want %q", got, want)
	}
	if got, want := strings.Join(flags, " "), "--payload {} --method POST --async"; got != want {
		t.Fatalf("flags = %q, want %q", got, want)
	}
	if looksLikeFlag("-1") {
		t.Fatal("looksLikeFlag(-1) = true, want numeric values to remain flag arguments")
	}
}

func TestCmdAppsRm_LongQuietAlias(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, "", http.StatusNoContent)
	if code := cmdAppsRm([]string{"--quiet", "demo"}); code != 0 {
		t.Fatalf("cmdAppsRm --quiet = %d, want 0", code)
	}
	if f.sawMethod != http.MethodDelete || f.sawPath != "/v1/apps/demo" {
		t.Errorf("request = %s %s, want DELETE /v1/apps/demo", f.sawMethod, f.sawPath)
	}
}

func TestCmdAppsDispatch_QuietDeleteSpellings(t *testing.T) {
	for _, spelling := range []string{"-q", "--quiet"} {
		t.Run(spelling, func(t *testing.T) {
			resetJSONOut(t)
			f := authedFakeAPI(t, "", http.StatusNoContent)
			if code := run([]string{"apps", spelling, "demo"}); code != 0 {
				t.Fatalf("run(apps %s demo) = %d, want 0", spelling, code)
			}
			if f.sawMethod != http.MethodDelete || f.sawPath != "/v1/apps/demo" {
				t.Errorf("request = %s %s, want DELETE /v1/apps/demo", f.sawMethod, f.sawPath)
			}
		})
	}
}

func TestCmdAppsDispatch_RejectsUnknownPositionals(t *testing.T) {
	for _, args := range [][]string{{"delete", "--help"}, {"typo"}, {"ls", "extra"}} {
		t.Run(strings.Join(args, "-"), func(t *testing.T) {
			resetJSONOut(t)
			f := authedFakeAPI(t, `[]`, http.StatusOK)
			if code := run(append([]string{"apps"}, args...)); code != 1 {
				t.Fatalf("run(apps %s) = %d, want usage error 1", strings.Join(args, " "), code)
			}
			if f.sawMethod != "" {
				t.Fatalf("unknown apps positional made %s %s request", f.sawMethod, f.sawPath)
			}
		})
	}
}

func TestBuildHelpListsEveryImplementedSubcommand(t *testing.T) {
	resetJSONOut(t)
	var out bytes.Buffer
	oldOut := osStdout
	osStdout = &out
	t.Cleanup(func() { osStdout = oldOut })

	if code := run([]string{"build", "--help"}); code != 0 {
		t.Fatalf("build --help = %d, want 0", code)
	}
	for _, sub := range []string{"status", "list", "provenance", "sbom"} {
		if !strings.Contains(out.String(), "  "+sub) {
			t.Errorf("build help missing %q:\n%s", sub, out.String())
		}
	}
}

func TestKeysAddHelpNeverCreatesCredential(t *testing.T) {
	resetJSONOut(t)
	for _, help := range []string{"-h", "--help"} {
		t.Run(help, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				http.Error(w, "mutation must not run", http.StatusInternalServerError)
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "test-token")
			var stdout bytes.Buffer
			oldOut := osStdout
			osStdout = &stdout
			t.Cleanup(func() { osStdout = oldOut })
			if code := run([]string{"keys", "add", help}); code != 0 {
				t.Fatalf("keys add %s = %d, want 0", help, code)
			}
			if calls != 0 {
				t.Fatalf("keys add %s made %d API request(s)", help, calls)
			}
			if !strings.Contains(stdout.String(), "gregale keys") || !strings.Contains(stdout.String(), "add") {
				t.Fatalf("help output = %q", stdout.String())
			}
		})
	}
}

func TestNestedHelpNeverDispatchesMutatingCommands(t *testing.T) {
	resetJSONOut(t)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, "mutation must not run", http.StatusInternalServerError)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")

	for _, args := range [][]string{
		{"keys", "add", "--help"},
		{"apps", "--quiet", "--help"},
		{"secrets", "set", "--help"},
		{"env", "push", "--help"},
		{"github", "disconnect", "--help"},
	} {
		t.Run(strings.Join(args[:2], "_"), func(t *testing.T) {
			var stdout bytes.Buffer
			oldOut := osStdout
			osStdout = &stdout
			t.Cleanup(func() { osStdout = oldOut })
			if code := run(args); code != 0 {
				t.Fatalf("run(%v) = %d, want help success", args, code)
			}
			if !strings.Contains(stdout.String(), "Usage:") {
				t.Fatalf("run(%v) output = %q", args, stdout.String())
			}
		})
	}
	if calls != 0 {
		t.Fatalf("nested help made %d API request(s)", calls)
	}
}

func TestGeneratedHelpUsesDispatcherArgumentOrder(t *testing.T) {
	for _, tc := range []struct {
		command string
		want    string
		reject  string
	}{
		{command: "github", want: "gregale github <status|sync|repos|bind|disconnect> <slug>", reject: "gregale github <slug> <"},
		{command: "debug", want: "gregale debug <requests|coverage|running|regressions|compare|bundle> [flags] <slug> [<request-id>]", reject: "gregale debug <slug> <"},
		{command: "audit-events", want: "gregale audit-events <list|get> [<id>]", reject: "gregale audit-events <id> <"},
	} {
		t.Run(tc.command, func(t *testing.T) {
			var stdout bytes.Buffer
			oldOut := osStdout
			osStdout = &stdout
			t.Cleanup(func() { osStdout = oldOut })
			if code := run([]string{tc.command, "--help"}); code != 0 {
				t.Fatalf("%s --help = %d", tc.command, code)
			}
			if got := stdout.String(); !strings.Contains(got, tc.want) || strings.Contains(got, tc.reject) {
				t.Fatalf("help output = %q, want %q and not %q", got, tc.want, tc.reject)
			}
		})
	}
}

func TestLogsHelpDocumentsRequiredSlugAndFilters(t *testing.T) {
	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	t.Cleanup(func() { osStdout = oldOut })
	if code := run([]string{"logs", "--help"}); code != 0 {
		t.Fatalf("logs --help = %d", code)
	}
	for _, want := range []string{"gregale logs <slug>", "--deployment", "--grep", "--since", "--level", "--explain", "--follow"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("logs help missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestCLIContract_PaginationBounds(t *testing.T) {
	cronID := "0123456789abcdef0123456789abcdef"
	cases := []struct {
		name string
		fn   func() int
	}{
		{name: "deployments", fn: func() int { return cmdDeployments([]string{"--limit", "0"}) }},
		{name: "invocations", fn: func() int { return cmdInvocationsList([]string{"--limit", "0"}) }},
		{name: "queue", fn: func() int { return cmdQueuePeek([]string{"demo", "--limit", "0"}) }},
		{name: "crons", fn: func() int { return cmdCronsRuns([]string{cronID, "--limit", "0"}) }},
		{name: "workflows", fn: func() int { return cmdWorkflowsList([]string{"--app", "demo", "--limit", "0"}) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code := tc.fn(); code != 1 {
				t.Fatalf("invalid --limit exit = %d, want 1", code)
			}
		})
	}
}

func TestValidateCLILimitAndOffset(t *testing.T) {
	for _, tc := range []struct {
		name string
		got  error
		want bool
	}{
		{name: "limit zero", got: validateCLILimit("limit", 0, 100), want: true},
		{name: "limit negative", got: validateCLILimit("limit", -1, 100), want: true},
		{name: "limit above max", got: validateCLILimit("limit", 101, 100), want: true},
		{name: "limit boundary", got: validateCLILimit("limit", 100, 100), want: false},
		{name: "offset negative", got: validateCLIOffset("offset", -1), want: true},
		{name: "offset zero", got: validateCLIOffset("offset", 0), want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if (tc.got != nil) != tc.want {
				t.Errorf("error = %v, want error=%t", tc.got, tc.want)
			}
		})
	}
}
