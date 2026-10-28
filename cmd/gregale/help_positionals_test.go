package main_test

import (
	"bytes"
	"github.com/onebox-faas/faas/cmd/gregale"
	"strings"
	"testing"
)

// Prod hunt #3: 153 leaves printed a --help synopsis without the positional
// arguments their own usage error demands (`crons info` showed no <id>,
// `triggers retry` no <trigger-id> <record-id>), and nested groups such as
// `orgs members` listed no verbs at all.
func TestLeafHelpNamesPositionalArguments(t *testing.T) {
	cases := []struct {
		path string
		want []string
	}{
		{"crons info", []string{"gregale cron get <id>"}},
		{"crons cancel", []string{"gregale cron cancel <cron-id> <run-id>"}},
		{"crons runs", []string{"gregale cron runs <id>"}},
		{"triggers retry", []string{"gregale trigger retry <trigger-id> <record-id>"}},
		{"jobs artifact-url", []string{"gregale job artifact-url <name> <run-id> <task-index> <artifact-name>"}},
		{"build status", []string{"gregale build status <id>"}},
		{"deploys cancel", []string{"gregale release cancel <id>"}},
		{"domains verify", []string{"gregale domain verify <domain>"}},
		{"edge-rules rm", []string{"gregale edge rule delete <id>"}},
		{"traffic status", []string{"gregale release traffic status <slug>"}},
		{"trusted-publishers add", []string{"gregale registry key create <slug> <name> <pub.pem>"}},
		{"cors ls", []string{"gregale edge cors list [<slug>]"}},
		{"workers status", []string{"gregale worker status [<app>]"}},
		{"issues get", []string{"gregale debug issue get --app <SLUG> <issue-id>"}},
		{"runs cancel", []string{"gregale sandbox cancel <id>"}},
		{"config set", []string{"gregale config set <api-base|json> <value>"}},
		{"cache purge", []string{"gregale edge cache purge [<slug>]"}},
		{"platform-tenants link-consumer", []string{"--id <UUID>", "--consumer-id <UUID>"}},
		{"orgs members", []string{"invite", "change-role"}},
		{"orgs members invite", []string{"--org <SLUG>", "--email <ADDR>"}},
		{"orgs keys info", []string{"gregale org keys info --org <SLUG> <key-id>"}},
		{"realtime auth", []string{"rotate", "finalize", "status"}},
		{"queue bindings update", []string{"gregale queue bindings update <slug> <binding-id>"}},
		{"jobs registry set", []string{"--registry <HOST>", "--user <USER>", "<job>"}},
		// Prod hunt #9: rename help omitted the required new slug.
		{"app my-api rename", []string{"gregale app <slug> rename <new-slug>", "old hostname stops routing"}},
		// Prod hunt #9: park silently suspends a min-instances floor.
		{"park", []string{"gregale app park <slug>", "min-instances floor stays off"}},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			main.ResetJSONOut(t)
			var stdout bytes.Buffer
			oldOut := (*main.OsStdout)
			(*main.OsStdout) = &stdout
			t.Cleanup(func() { (*main.OsStdout) = oldOut })
			if code := main.Run(append(strings.Fields(tc.path), "--help")); code != 0 {
				t.Fatalf("help exit = %d", code)
			}
			for _, want := range tc.want {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("%s --help is missing %q:\n%s", tc.path, want, stdout.String())
				}
			}
		})
	}
}
