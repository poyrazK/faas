package main

import (
	"bytes"
	"net/http"
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
