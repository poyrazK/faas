package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// confirmAuditAPI is a fake apid for the CLI audit 2026-10-31 tests: reads
// answer with a Pro account, and every request is recorded so a test can
// prove a refused command changed nothing.
type confirmAuditAPI struct {
	mu       sync.Mutex
	requests []string
}

func (a *confirmAuditAPI) mutations() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	for _, r := range a.requests {
		if !strings.HasPrefix(r, http.MethodGet+" ") {
			out = append(out, r)
		}
	}
	return out
}

func newConfirmAuditAPI(t *testing.T) *confirmAuditAPI {
	t.Helper()
	fake := &confirmAuditAPI{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fake.mu.Lock()
		fake.requests = append(fake.requests, r.Method+" "+r.URL.Path)
		fake.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/account/export":
			_ = json.NewEncoder(w).Encode(api.AccountExportResponse{ExportedAt: "2026-10-31T10:00:00Z", Account: api.AccountResponse{Email: "a@b.c", Plan: "pro"}})
		case r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(api.AccountResponse{Email: "a@b.c", Plan: "pro"})
		case r.URL.Path == "/v1/billing/cancel":
			_ = json.NewEncoder(w).Encode(api.BillingCancelResponse{CancelScheduled: true})
		case strings.HasSuffix(r.URL.Path, "/edge-rules/rollback"):
			_, _ = w.Write([]byte(`[]`))
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	t.Setenv("FAAS_JSON", "0")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Chdir(t.TempDir())
	main.ResetJSONOut(t)
	return fake
}

func setStdinTTY(t *testing.T, tty bool) {
	t.Helper()
	old := *main.TestOnlyTTY
	*main.TestOnlyTTY = &tty
	t.Cleanup(func() { *main.TestOnlyTTY = old })
}

func runAudit(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	stdout, restoreOut := main.CaptureStdout(t)
	stderr, restoreErr := main.CaptureStderr(t)
	code := main.Run(args)
	restoreErr()
	restoreOut()
	return code, stdout.String(), stderr.String()
}

func onlyProblem(t *testing.T, stderr string) api.Problem {
	t.Helper()
	var problem api.Problem
	if err := json.Unmarshal([]byte(strings.TrimSpace(stderr)), &problem); err != nil {
		t.Fatalf("stderr is not one Problem: %v\n%s", err, stderr)
	}
	return problem
}

// Findings 1, 2 and 4: JSON mode used to skip the confirmation of billing
// cancel and env rollback and carry them out; org delete and the plan
// downgrade printed "aborted" on stdout. Every one now refuses up front.
func TestConfirmingCommandsRefuseInJSONMode(t *testing.T) {
	cases := [][]string{
		{"billing", "cancel"},
		{"env", "rollback", "1", "--app", "demo"},
		{"org", "delete", "acme"},
		{"billing", "plan", "free"},
		{"edge", "rule", "rollback", "--app", "demo", "--to", "3"},
		{"account", "delete"},
	}
	for _, args := range cases {
		for _, viaEnv := range []bool{false, true} {
			name := strings.Join(args, " ")
			if viaEnv {
				name += "/FAAS_JSON"
			}
			t.Run(name, func(t *testing.T) {
				fake := newConfirmAuditAPI(t)
				main.PipeStdin(t, "y\n")
				setStdinTTY(t, true)
				argv := append([]string(nil), args...)
				if viaEnv {
					t.Setenv("FAAS_JSON", "1")
				} else {
					argv = append(argv, "--json")
				}
				code, stdout, stderr := runAudit(t, argv...)
				problem := onlyProblem(t, stderr)
				if code != 1 || stdout != "" || problem.Title != "Confirmation required" || !strings.Contains(problem.Detail, "--yes") {
					t.Fatalf("exit=%d stdout=%q problem=%+v", code, stdout, problem)
				}
				if m := fake.mutations(); len(m) != 0 {
					t.Fatalf("refused command still sent %v", m)
				}
			})
		}
	}
}

// Findings 1 and 4: without a terminal there is nobody to answer the y/N
// prompt, so it refuses; at a terminal a declined prompt is a Problem on
// stderr and an accepted one proceeds.
func TestYesNoPromptsRefuseWithoutATerminal(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		tty       bool
		input     string
		wantCode  int
		wantTitle string
		mutates   bool
	}{
		{"billing cancel, piped stdin", []string{"billing", "cancel"}, false, "y\n", 1, "Confirmation required", false},
		{"org delete, piped stdin", []string{"org", "delete", "acme"}, false, "y\n", 1, "Confirmation required", false},
		{"plan downgrade, piped stdin", []string{"billing", "plan", "free"}, false, "y\n", 1, "Confirmation required", false},
		{"billing cancel, declined", []string{"billing", "cancel"}, true, "n\n", 1, "Aborted", false},
		{"org delete, end of input", []string{"org", "delete", "acme"}, true, "", 1, "Aborted", false},
		{"billing cancel, accepted", []string{"billing", "cancel"}, true, "y\n", 0, "", true},
		{"org delete, accepted", []string{"org", "delete", "acme"}, true, "yes\n", 0, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := newConfirmAuditAPI(t)
			main.PipeStdin(t, tc.input)
			setStdinTTY(t, tc.tty)
			code, stdout, stderr := runAudit(t, tc.args...)
			if code != tc.wantCode {
				t.Fatalf("exit=%d, want %d\nstdout=%q\nstderr=%q", code, tc.wantCode, stdout, stderr)
			}
			if tc.wantTitle != "" && (!strings.Contains(stderr, tc.wantTitle) || strings.Contains(stdout, "aborted")) {
				t.Fatalf("stderr=%q stdout=%q, want %q on stderr only", stderr, stdout, tc.wantTitle)
			}
			if got := len(fake.mutations()) != 0; got != tc.mutates {
				t.Fatalf("mutations=%v, want mutation=%v", fake.mutations(), tc.mutates)
			}
		})
	}
}

// Finding 5: flags after the slug failed with a usage error; finding 6:
// --yes is accepted wherever a command confirms.
func TestConfirmationFlagsAcceptedAfterPositionals(t *testing.T) {
	cases := [][]string{
		{"org", "delete", "acme", "-q"},
		{"org", "delete", "acme", "--yes"},
		{"org", "delete", "acme", "--quiet"},
		{"org", "delete", "-q", "acme"},
		{"edge", "rule", "rollback", "--app", "demo", "--to", "3", "--yes"},
		{"billing", "cancel", "--yes"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			fake := newConfirmAuditAPI(t)
			setStdinTTY(t, false)
			code, stdout, stderr := runAudit(t, args...)
			if code != 0 || len(fake.mutations()) != 1 {
				t.Fatalf("exit=%d mutations=%v\nstdout=%q\nstderr=%q", code, fake.mutations(), stdout, stderr)
			}
		})
	}
}

// Finding 7: --json printed prose on stdout, and an existing -o file was
// replaced silently.
func TestAccountExportReceiptAndNoClobber(t *testing.T) {
	t.Run("json receipt", func(t *testing.T) {
		newConfirmAuditAPI(t)
		out := filepath.Join(t.TempDir(), "bundle.json")
		code, stdout, stderr := runAudit(t, "account", "export", "-o", out, "--json")
		var receipt struct {
			Path            string `json:"path"`
			SecretsIncluded bool   `json:"secrets_included"`
		}
		if err := json.Unmarshal([]byte(stdout), &receipt); err != nil || code != 0 || receipt.Path != out || !receipt.SecretsIncluded {
			t.Fatalf("exit=%d receipt=%+v err=%v\nstdout=%q\nstderr=%q", code, receipt, err, stdout, stderr)
		}
	})
	for _, tc := range []struct {
		name     string
		force    bool
		wantCode int
		wantBody string
	}{
		{"existing file kept", false, 1, "keep me"},
		{"existing file replaced with --force", true, 0, "a@b.c"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := newConfirmAuditAPI(t)
			out := filepath.Join(t.TempDir(), "bundle.json")
			if err := os.WriteFile(out, []byte("keep me"), 0o600); err != nil {
				t.Fatal(err)
			}
			args := []string{"account", "export", "-o", out}
			if tc.force {
				args = append(args, "--force")
			}
			code, _, stderr := runAudit(t, args...)
			body, _ := os.ReadFile(out)
			if code != tc.wantCode || !strings.Contains(string(body), tc.wantBody) {
				t.Fatalf("exit=%d body=%q\nstderr=%q", code, body, stderr)
			}
			if !tc.force && (len(fake.requests) != 0 || !strings.Contains(stderr, "--force")) {
				t.Fatalf("refusal sent %v or did not name --force: %q", fake.requests, stderr)
			}
		})
	}
}

// Finding 8: these usage errors were plain text (or on stdout) under --json,
// and `logs view save --zz-bogus` saved a view named "--zz-bogus".
func TestUsageErrorsAreProblemsInJSONMode(t *testing.T) {
	cases := [][]string{
		{"build", "audit-not-a-verb"},
		{"env", "audit-not-a-verb"},
		{"org", "members", "audit-not-a-verb"},
		{"org", "invitation", "audit-not-a-verb"},
		{"billing", "audit-not-a-verb"},
		{"billing", "status", "--zz-bogus"},
		{"account", "mfa", "audit-not-a-verb"},
		{"queue", "bindings", "list"},
		{"logs", "view", "save", "--zz-bogus"},
		{"logs", "view", "use", "errors"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			fake := newConfirmAuditAPI(t)
			code, stdout, stderr := runAudit(t, append(append([]string(nil), args...), "--json")...)
			problem := onlyProblem(t, stderr)
			if code != 1 || stdout != "" || problem.Status != http.StatusBadRequest {
				t.Fatalf("exit=%d stdout=%q problem=%+v", code, stdout, problem)
			}
			if m := fake.mutations(); len(m) != 0 {
				t.Fatalf("usage error sent %v", m)
			}
		})
	}
	newConfirmAuditAPI(t)
	if code, stdout, _ := runAudit(t, "logs", "view", "list", "--json"); code != 0 || strings.Contains(stdout, "zz-bogus") {
		t.Fatalf("a flag was saved as a view name: exit=%d %s", code, stdout)
	}
}

// Finding 9: exit 2 means authentication (docs/cli-config.md), not usage.
func TestUsageErrorsExitOne(t *testing.T) {
	for _, args := range [][]string{
		{"dev", "setup", "--bogus"},
		{"test", "coverage", "report", "--bogus"},
		{"test", "recovery", "report", "--bogus"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			newConfirmAuditAPI(t)
			if code, _, stderr := runAudit(t, args...); code != 1 {
				t.Fatalf("exit=%d, want 1\n%s", code, stderr)
			}
		})
	}
}
