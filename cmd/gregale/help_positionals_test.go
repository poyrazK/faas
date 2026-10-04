package main

import (
	"bytes"
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
		{"crons info", []string{"gregale crons info <id>"}},
		{"crons cancel", []string{"gregale crons cancel <cron-id> <run-id>"}},
		{"crons runs", []string{"gregale crons runs <id>"}},
		{"triggers retry", []string{"gregale triggers retry <trigger-id> <record-id>"}},
		{"jobs artifact-url", []string{"gregale jobs artifact-url <name> <run-id> <task-index> <artifact-name>"}},
		{"build status", []string{"gregale build status <id>"}},
		{"deploys cancel", []string{"gregale deploys cancel <id>"}},
		{"domains verify", []string{"gregale domains verify <domain>"}},
		{"edge-rules rm", []string{"gregale edge-rules rm <id>"}},
		{"traffic status", []string{"gregale traffic status <slug>"}},
		{"trusted-publishers add", []string{"gregale trusted-publishers add <slug> <name> <pub.pem>"}},
		{"cors ls", []string{"gregale cors ls [<slug>]"}},
		{"workers status", []string{"gregale workers status [<app>]"}},
		{"issues get", []string{"gregale issues get --app <SLUG> <issue-id>"}},
		{"runs cancel", []string{"gregale runs cancel <id>"}},
		{"config set", []string{"gregale config set <api-base|json> <value>"}},
		{"cache purge", []string{"gregale cache purge <slug>"}},
		{"platform-tenants link-consumer", []string{"--id <UUID>", "--consumer-id <UUID>"}},
		{"orgs members", []string{"invite", "change-role"}},
		{"orgs members invite", []string{"--org <SLUG>", "--email <ADDR>"}},
		{"orgs keys info", []string{"gregale orgs keys info --org <SLUG> <key-id>"}},
		{"realtime auth", []string{"rotate", "finalize", "status"}},
		{"queue bindings update", []string{"gregale queue bindings update <slug> <binding-id>"}},
		{"jobs registry set", []string{"--registry <HOST>", "--user <USER>", "<job>"}},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			resetJSONOut(t)
			var stdout bytes.Buffer
			oldOut := osStdout
			osStdout = &stdout
			t.Cleanup(func() { osStdout = oldOut })
			if code := run(append(strings.Fields(tc.path), "--help")); code != 0 {
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
