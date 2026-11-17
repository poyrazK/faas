package journeys

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// GA row 156: verify actual saved list receipts with a built CLI and no reads
// from the API during verification, including tampering and malformed input.
func TestExportVerificationIsolatedCLI(t *testing.T) {
	cli := os.Getenv("GREGALE_JOURNEY_TEST_CLI")
	if tgt != nil {
		cli = tgt.cli
	}
	if cli == "" {
		t.Skip("set GREGALE_JOURNEY_TEST_CLI or configure a journey target")
	}
	for _, kind := range []string{"build", "invocation"} {
		t.Run(kind, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				key, path := "items", "/v1/builds"
				if kind == "invocation" {
					key, path = "invocations", "/v1/invocations"
				}
				if r.Method != "GET" || r.URL.Path != path {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
					http.NotFound(w, r)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{key: []map[string]string{{"id": "first"}, {"id": "second"}}})
			}))
			t.Cleanup(server.Close)
			tg := &target{api: server.URL, cli: cli, home: t.TempDir(), appsDomain: "apps.example.test"}
			data, receipt := filepath.Join(tg.home, "rows.jsonl"), filepath.Join(tg.home, "receipt.json")
			run := func(offline bool, args ...string) (int, string, string) {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, cli, args...)
				// cliEnv sets FAAS_JSON=1; the human mode must get text, so
				// JSON output comes only from an explicit --json here.
				cmd.Env, cmd.Dir = append(tg.cliEnv("fp_test_export-verify"), "FAAS_JSON=0"), tg.home
				if offline {
					cmd.Env = append(cmd.Env, "FAAS_TOKEN=")
				}
				var out, stderr bytes.Buffer
				cmd.Stdout, cmd.Stderr = &out, &stderr
				err := cmd.Run()
				if ctx.Err() != nil {
					t.Fatal(ctx.Err())
				}
				code := 0
				if err != nil {
					var exit *exec.ExitError
					if !errors.As(err, &exit) {
						t.Fatal(err)
					}
					code = exit.ExitCode()
				}
				return code, out.String(), stderr.String()
			}
			code, out, stderr := run(false, "--json", kind, "list", "--all", "--output", data)
			if code != 0 || stderr != "" || calls.Load() != 1 {
				t.Fatalf("exit=%d calls=%d out=%s err=%s", code, calls.Load(), out, stderr)
			}
			if err := os.WriteFile(receipt, []byte(out), 0600); err != nil {
				t.Fatal(err)
			}
			original, err := os.ReadFile(data)
			if err != nil {
				t.Fatal(err)
			}
			for _, mode := range []string{"json", "human", "mismatch", "malformed", "invalid-receipt"} {
				want := 0
				switch mode {
				case "mismatch":
					if err := os.WriteFile(data, append(append([]byte{}, original...), []byte("{}\n")...), 0600); err != nil {
						t.Fatal(err)
					}
					want = 1
				case "malformed":
					if err := os.WriteFile(data, []byte("{}\n{\"secret\":credential-secret}\n"), 0600); err != nil {
						t.Fatal(err)
					}
					want = 1
				case "invalid-receipt":
					if err := os.WriteFile(receipt, []byte("{\"secret\":credential-secret}"), 0600); err != nil {
						t.Fatal(err)
					}
					want = 1
				}
				args := []string{"--json", "export", "verify", data, "--receipt", receipt}
				if mode == "human" {
					args = args[1:]
				}
				code, out, stderr = run(true, args...)
				if code != want || calls.Load() != 1 || strings.Contains(out+stderr, "credential-secret") {
					t.Fatalf("mode=%s exit=%d calls=%d out=%s err=%s", mode, code, calls.Load(), out, stderr)
				}
				switch mode {
				case "human":
					if stderr != "" || !strings.HasPrefix(out, "Export verified:") {
						t.Fatalf("out=%s err=%s", out, stderr)
					}
				case "malformed", "invalid-receipt":
					var problem map[string]json.RawMessage
					if out != "" || json.Unmarshal([]byte(stderr), &problem) != nil || string(problem["exit_code"]) != "1" || problem["pagination"] != nil || problem["cli"] != nil {
						t.Fatalf("out=%s err=%s", out, stderr)
					}
					if mode == "malformed" && !strings.Contains(stderr, "line 2") {
						t.Fatal(stderr)
					}
				default:
					var report struct {
						SchemaVersion int `json:"schema_version"`
						Verified      bool
						Observed      struct {
							ItemsEmitted int64 `json:"items_emitted"`
							BytesWritten int64 `json:"bytes_written"`
							SHA256       string
						}
					}
					if stderr != "" || json.Unmarshal([]byte(out), &report) != nil || report.SchemaVersion != 1 || report.Verified != (mode == "json") {
						t.Fatalf("out=%s err=%s", out, stderr)
					}
					if mode == "json" && (report.Observed.ItemsEmitted != 2 || report.Observed.BytesWritten != int64(len(original)) || len(report.Observed.SHA256) != 64) {
						t.Fatal(out)
					}
				}
			}
		})
	}
}
