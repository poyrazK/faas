package main_test

import (
	"bytes"
	"github.com/onebox-faas/faas/cmd/gregale"
	"strings"
	"testing"
)

// TestPostgresHelpShowsArgumentsAndParsedFlags pins the production-us hunt #9
// finding that `gregale postgres create --help` printed no NAME, `get`,
// `delete`, `restore` and `attach` hid their positionals, and
// `compute-policy` plus `bindings list|create|get|delete` were missing from
// help entirely, so the documented usage could not be run as shown.
func TestPostgresHelpShowsArgumentsAndParsedFlags(t *testing.T) {
	oldOut := (*main.OsStdout)
	t.Cleanup(func() { (*main.OsStdout) = oldOut })
	parsed := map[string][]string{}
	for _, file := range []string{"commands_postgres.go", "commands_postgres_compute_policy.go", "commands_postgres_resize.go"} {
		for fn, flags := range flagsRegisteredIn(t, file) {
			parsed[fn] = append(parsed[fn], flags...)
		}
	}
	aliases := map[string]bool{"environment-key": true} // attach keeps the bindings-create spelling as a hidden alias
	cases := []struct {
		args        []string
		fn          string
		positionals []string
	}{
		{args: []string{"create"}, fn: "cmdPostgresCreate", positionals: []string{"<name>"}},
		{args: []string{"get"}, positionals: []string{"<database>"}},
		{args: []string{"delete"}, positionals: []string{"<database>"}},
		{args: []string{"restore"}, fn: "cmdPostgresRestore", positionals: []string{"<database>"}},
		{args: []string{"resize"}, fn: "cmdPostgresResize", positionals: []string{"<database>"}},
		{args: []string{"compute-policy"}, fn: "cmdPostgresComputePolicy", positionals: []string{"<database>"}},
		{args: []string{"compute-policy-status"}, positionals: []string{"<database>", "<request_uuid>"}},
		{args: []string{"attach"}, fn: "cmdPostgresAttach", positionals: []string{"<database>", "<app>"}},
		{args: []string{"bindings", "list"}, positionals: []string{"<database>"}},
		{args: []string{"bindings", "create"}, fn: "cmdPostgresBindingsCreate", positionals: []string{"<database_id>"}},
		{args: []string{"bindings", "get"}, positionals: []string{"<id>"}},
		{args: []string{"bindings", "delete"}, positionals: []string{"<id>"}},
		{args: []string{"bindings", "rotate"}, positionals: []string{"<id>"}},
	}
	for _, tc := range cases {
		name := strings.Join(tc.args, " ")
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			(*main.OsStdout) = &out
			args := append(append([]string{"postgres"}, tc.args...), "--help")
			if code := main.Run(args); code != 0 {
				t.Fatalf("postgres %s --help = %d", name, code)
			}
			help := out.String()
			for _, positional := range tc.positionals {
				if !strings.Contains(help, positional) {
					t.Errorf("postgres %s --help omits %s:\n%s", name, positional, help)
				}
			}
			if tc.fn == "" {
				return
			}
			if len(parsed[tc.fn]) == 0 {
				t.Fatalf("found no flags registered in %s", tc.fn)
			}
			for _, flag := range parsed[tc.fn] {
				if aliases[flag] {
					continue
				}
				if !strings.Contains(help, "--"+flag+" ") && !strings.Contains(help, "--"+flag+"[") {
					t.Errorf("postgres %s parses --%s but its help omits it", name, flag)
				}
			}
		})
	}
}
