package main_test

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// CLI audit 2026-11-05: bucket list, github status and edge cors get refused
// --app, and org commands took the org either as an argument or as --org but
// not both. A command that takes the app (or, under orgs, the org) as its
// first argument also takes the flag, and the other way round.
func TestAppAndOrgTargetsTakeEitherSpelling(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
		err  bool
	}{
		{"bucket list --app", []string{"bucket", "list", "--app", "my-api"}, []string{"bucket", "list", "my-api"}, false},
		{"github status --app=", []string{"github", "status", "--app=my-api", "--json"}, []string{"github", "status", "my-api", "--json"}, false},
		{"cors get --app", []string{"cors", "get", "--app", "my-api"}, []string{"cors", "get", "my-api"}, false},
		{"same app both ways", []string{"bucket", "list", "my-api", "--app", "my-api"}, []string{"bucket", "list", "my-api"}, false},
		{"two apps", []string{"bucket", "list", "a", "--app", "b"}, nil, true},
		{"orgs members list --org", []string{"orgs", "members", "list", "--org", "acme"}, []string{"orgs", "members", "list", "acme"}, false},
		{"orgs keys list ORG", []string{"orgs", "keys", "list", "acme"}, []string{"orgs", "keys", "list", "--org", "acme"}, false},
		// A preview slug is not an app: --app is left for the parser.
		{"previews show keeps --app", []string{"previews", "show", "pr-1", "--app", "my-api"}, []string{"previews", "show", "pr-1", "--app", "my-api"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := main.AppArgumentAsFlag(tc.in)
			if err == nil {
				got, err = main.AppFlagAsArgument(got)
			}
			if tc.err {
				if err == nil {
					t.Fatalf("%v = %v, want a conflict error", tc.in, got)
				}
				return
			}
			if err != nil || !slices.Equal(got, tc.want) {
				t.Fatalf("%v = %v, %v; want %v", tc.in, got, err, tc.want)
			}
		})
	}
}

// `gregale job get` with no saved Job printed the raw
// "lstat …/jobs.json: no such file or directory" (CLI audit 2026-11-05).
func TestJobInfoWithoutSavedJobExplainsWhatToDo(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("FAAS_TOKEN", "gk_test")
	t.Setenv("FAAS_API", "http://127.0.0.1:9")
	main.ResetJSONOut(t)
	var stderr bytes.Buffer
	old := *main.OsStderr
	*main.OsStderr = &stderr
	t.Cleanup(func() { *main.OsStderr = old })
	if code := main.Run([]string{"jobs", "info"}); code == 0 {
		t.Fatal("jobs info without a saved Job exited 0")
	}
	if out := stderr.String(); strings.Contains(out, "lstat") || !strings.Contains(out, "No saved Job") {
		t.Fatalf("jobs info without a saved Job = %q, want the No saved Job guidance", out)
	}
}
