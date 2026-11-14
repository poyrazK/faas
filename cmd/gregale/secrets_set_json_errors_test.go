package main_test

import (
	"encoding/json"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// TestEnvSetLocalValidationErrorsAreJSONProblems: on rc.324 a rejected
// key name or a line without "=" printed plain stderr and exited 1 even in
// global JSON mode, so agents needed a plaintext fallback (#6662). Every
// local parse failure now renders the same Problem envelope as any other
// validation error, before any request, without echoing the input.
func TestEnvSetLocalValidationErrorsAreJSONProblems(t *testing.T) {
	const sentinel = "dummy-sentinel-6662"
	cases := []struct {
		name     string
		args     []string
		stdin    string
		wantCode string
		detail   string
	}{
		{"positional invalid key", []string{"1BAD=" + sentinel}, "", "secret_invalid_key", "must start with a letter"},
		{"positional missing equals", []string{"QA_TOKEN"}, "", "invalid_request", "KEY=VALUE"},
		{"stdin missing equals", []string{"--from-stdin"}, "QA_TOKEN " + sentinel + "\n", "invalid_request", "stdin line 1"},
		{"stdin invalid key", []string{"--from-stdin"}, "# comment\n1BAD=" + sentinel + "\n", "secret_invalid_key", "must start with a letter"},
	}
	for _, tc := range cases {
		for _, mode := range []string{"human", "flag", "env"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				t.Chdir(t.TempDir())
				t.Setenv("FAAS_API", "http://127.0.0.1:9") // nothing listens: any request would fail as transport
				t.Setenv("FAAS_TOKEN", "test-token")
				t.Setenv("FAAS_JSON", "")
				main.ResetJSONOut(t)
				argv := []string{}
				switch mode {
				case "flag":
					argv = append(argv, "--json")
				case "env":
					t.Setenv("FAAS_JSON", "1")
				}
				argv = append(argv, "--non-interactive", "env", "set", "--app", "json-errors-demo", "--dry-run")
				argv = append(argv, tc.args...)
				main.PipeStdin(t, tc.stdin)
				code, stdout, stderr := runAudit(t, argv...)
				if code != 1 || stdout != "" {
					t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
				}
				if strings.Contains(stderr, sentinel) {
					t.Fatalf("rejected input echoed its value: %q", stderr)
				}
				if mode == "human" {
					if strings.HasPrefix(strings.TrimSpace(stderr), "{") || !strings.Contains(stderr, tc.detail) {
						t.Fatalf("human stderr = %q, want readable text naming %q", stderr, tc.detail)
					}
					return
				}
				problem := onlyProblem(t, stderr)
				var envelope struct {
					ExitCode int    `json:"exit_code"`
					Category string `json:"category"`
				}
				if err := json.Unmarshal([]byte(strings.TrimSpace(stderr)), &envelope); err != nil {
					t.Fatal(err)
				}
				if problem.Code != tc.wantCode || problem.Status != 400 || !strings.Contains(problem.Detail, tc.detail) || envelope.ExitCode != code {
					t.Fatalf("problem=%+v envelope=%+v exit=%d, want code %s naming %q", problem, envelope, code, tc.wantCode, tc.detail)
				}
			})
		}
	}
}
