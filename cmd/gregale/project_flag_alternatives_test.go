package main_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

// GREG-02: a project deploy refused --startup-deadline-s and
// --no-require-authn with "project deploy policy is not yet supported",
// which hid the supported paths (gregale.yaml lifecycle, app update).
func TestProjectDeployRefusalNamesTheSupportedPath(t *testing.T) {
	cases := []struct {
		flags []string
		want  []string
	}{
		{[]string{"--startup-deadline-s", "120"}, []string{"--startup-deadline-s applies to a single app", "startup_deadline_s: 120", "gregale.yaml"}},
		{[]string{"--no-require-authn"}, []string{"gregale app update <app> --no-require-authn", "anyone on the internet"}},
		{[]string{"--max-retries", "3", "--reason", "x"}, []string{"--reason, --max-retries apply to a single app", "--max-retries: set lifecycle.max_retries"}},
	}
	for _, tc := range cases {
		t.Run(tc.flags[0], func(t *testing.T) {
			main.ResetJSONOut(t)
			t.Setenv("FAAS_API", "http://127.0.0.1:1")
			t.Setenv("FAAS_TOKEN", "fp_live_x")
			var stdout, stderr bytes.Buffer
			oldOut, oldErr := (*main.OsStdout), (*main.OsStderr)
			(*main.OsStdout), (*main.OsStderr) = &stdout, &stderr
			defer func() { (*main.OsStdout), (*main.OsStderr) = oldOut, oldErr }()

			args := append([]string{"--project", "--name", "demo", "--path", t.TempDir(), "--dry-run"}, tc.flags...)
			if code := main.CmdDeployTarball(args); code == 0 {
				t.Fatal("project deploy with a single-app flag succeeded")
			}
			got := stderr.String() + stdout.String()
			if strings.Contains(got, "not yet supported") {
				t.Errorf("refusal still says the policy is unsupported:\n%s", got)
			}
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("refusal lacks %q:\n%s", want, got)
				}
			}
		})
	}
}
