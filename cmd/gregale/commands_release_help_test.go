package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestReleaseManagementHelpPaths(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		wantUsage string
	}{
		{
			name:      "app scale leaf",
			args:      []string{"app", "example", "scale", "--help"},
			wantUsage: "usage: gregale app <slug> scale",
		},
		{
			name:      "rollouts parent",
			args:      []string{"rollouts", "--help"},
			wantUsage: "usage: gregale rollouts recover <slug>",
		},
		{
			name:      "rollouts recover leaf",
			args:      []string{"rollouts", "recover", "--help"},
			wantUsage: "usage: gregale rollouts recover <slug>",
		},
		{
			name:      "rollback",
			args:      []string{"rollback", "--help"},
			wantUsage: "usage: gregale rollback <slug>",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetJSONOut(t)
			var stdout, stderr bytes.Buffer
			oldOut, oldErr := osStdout, osStderr
			osStdout, osStderr = &stdout, &stderr
			t.Cleanup(func() { osStdout, osStderr = oldOut, oldErr })

			if code := run(tc.args); code != 0 {
				t.Fatalf("run(%q) = %d, want 0; stdout=%q stderr=%q", tc.args, code, stdout.String(), stderr.String())
			}
			if !strings.Contains(stdout.String(), tc.wantUsage) {
				t.Errorf("stdout %q does not contain %q", stdout.String(), tc.wantUsage)
			}
			if strings.Contains(stdout.String()+stderr.String(), "Usage of ") {
				t.Errorf("help rendered raw flag-set output: stdout=%q stderr=%q", stdout.String(), stderr.String())
			}
			if stderr.Len() != 0 {
				t.Errorf("help wrote to stderr: %q", stderr.String())
			}
		})
	}
}
