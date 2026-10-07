package main

import (
	"bytes"
	"strings"
	"testing"
)

// Prod hunt #3: these leaves' --help omitted the arguments and flags a
// customer needs to use them (edge-rules create listed only async/validate
// flags; cors allow, keys rotate, crons run, webhooks add, workflows run and
// realtime create listed none), and domains add printed "(required)" twice.
func TestLeafHelpDocumentsRequiredArguments(t *testing.T) {
	cases := []struct {
		path string
		want []string
	}{
		{"edge-rules create", []string{"--app <slug>", "--kind <KIND>", "--match-host <HOST>", "--throttle-requests-per-second <RPS>", "--redirect-to <URL>", "--cache-max-age-seconds <N>", "--budget-ms <MS>"}},
		{"cors allow", []string{"gregale cors allow <slug> <origin>", "--method <VERB>"}},
		{"cors rm", []string{"gregale cors rm [<slug>] <rule-id>"}},
		{"keys rotate", []string{"gregale keys rotate <key-id>"}},
		{"crons run", []string{"gregale crons run <cron-id>"}},
		{"webhooks add", []string{"--app <slug>", "--target-url <URL>", "--event <EVENT>"}},
		{"workflows run", []string{"<workflow-name>", "--app <slug>", "--input <JSON>"}},
		{"realtime create", []string{"<slug>", "--callback-url <URL>"}},
		{"alerts add", []string{"--app <slug>", "--name <NAME>", "--metric <METRIC>", "--threshold <N>", "--webhook-url <URL>"}},
		{"queue send", []string{"gregale queue send <slug>"}},
		{"jobs run", []string{"gregale jobs run <job-name>"}},
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
			if strings.Contains(stdout.String(), "(required) (required)") {
				t.Errorf("%s --help repeats (required):\n%s", tc.path, stdout.String())
			}
		})
	}
}

func TestDomainsAddHelpMarksRequiredOnce(t *testing.T) {
	resetJSONOut(t)
	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	t.Cleanup(func() { osStdout = oldOut })
	if code := run([]string{"domains", "add", "--help"}); code != 0 {
		t.Fatalf("help exit = %d", code)
	}
	if strings.Contains(stdout.String(), "(required) (required)") {
		t.Fatalf("domains add --help repeats (required):\n%s", stdout.String())
	}
}

// The workflow name can follow the flags, as the help synopsis shows.
func TestWorkflowsRunAcceptsNameAfterFlags(t *testing.T) {
	resetJSONOut(t)
	t.Setenv("FAAS_TOKEN", "")
	t.Setenv("FAAS_API", "http://127.0.0.1:1")
	_, readStderr, restore := swapIO(t)
	defer restore()
	_ = cmdWorkflowsRun([]string{"--app", "my-api", "nightly"})
	if stderr := readStderr(); strings.Contains(stderr, "usage: gregale workflows run") {
		t.Fatalf("flags-before-name was rejected as a usage error: %s", stderr)
	}
}
