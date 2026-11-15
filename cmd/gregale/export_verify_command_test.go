package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

func setupExportVerificationCLI(t *testing.T) *atomic.Int32 {
	t.Helper()
	main.SetupCLIRegression(t)
	t.Chdir(t.TempDir())
	t.Setenv("FAAS_JSON", "0")
	t.Setenv("FAAS_TOKEN", "")
	calls := new(atomic.Int32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "verification must be offline", 500)
	}))
	t.Cleanup(server.Close)
	t.Setenv("FAAS_API", server.URL)
	return calls
}

func runExportVerificationCLI(t *testing.T, args []string) (int, string, string) {
	t.Helper()
	out, stderr, restore := main.SwapIO(t)
	code := main.Run(args)
	text := stderr()
	restore()
	return code, out.String(), text
}

func TestExportVerificationCLIContracts(t *testing.T) {
	for _, mode := range []string{"json", "env", "human", "profile", "mismatch", "malformed", "bad-receipt", "help"} {
		t.Run(mode, func(t *testing.T) {
			calls := setupExportVerificationCLI(t)
			data := "{\"value\":9007199254740993}\n"
			path, receipt := filepath.Join(t.TempDir(), "rows.jsonl"), filepath.Join(t.TempDir(), "receipt.json")
			if err := os.WriteFile(path, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(receipt, []byte(integrityReceiptJSON(data, 1)), 0600); err != nil {
				t.Fatal(err)
			}
			args := []string{"--json", "export", "verify", path, "--receipt", receipt}
			want := 0
			switch mode {
			case "env":
				t.Setenv("FAAS_JSON", "1")
				args = args[1:]
			case "human":
				args = args[1:]
			case "profile":
				args = append([]string{"--profile", "missing-offline-profile"}, args...)
				t.Setenv("FAAS_API", "not-an-api-address")
			case "mismatch":
				if err := os.WriteFile(path, []byte(strings.Replace(data, "9007199254740993", "9007199254740994", 1)), 0600); err != nil {
					t.Fatal(err)
				}
				want = 1
			case "malformed":
				if err := os.WriteFile(path, []byte("{}\n{\"secret\":credential-secret}\n"), 0600); err != nil {
					t.Fatal(err)
				}
				want = 1
			case "bad-receipt":
				if err := os.WriteFile(receipt, []byte("{\"secret\":credential-secret}"), 0600); err != nil {
					t.Fatal(err)
				}
				want = 1
			case "help":
				args = []string{"--json", "help", "export", "verify"}
			}
			code, out, stderr := runExportVerificationCLI(t, args)
			if code != want || calls.Load() != 0 {
				t.Fatalf("exit=%d calls=%d out=%s err=%s", code, calls.Load(), out, stderr)
			}
			if strings.Contains(out+stderr, "credential-secret") || strings.Contains(out+stderr, "ignored-credential-path") {
				t.Fatal("input leaked")
			}
			switch mode {
			case "malformed", "bad-receipt":
				var problem map[string]json.RawMessage
				if out != "" || json.Unmarshal([]byte(stderr), &problem) != nil || string(problem["exit_code"]) != "1" || problem["pagination"] != nil || problem["cli"] != nil {
					t.Fatalf("out=%s err=%s", out, stderr)
				}
				if mode == "malformed" && !strings.Contains(stderr, "line 2") {
					t.Fatal(stderr)
				}
			case "human":
				if stderr != "" || !strings.HasPrefix(out, "Export verified: 1 records,") {
					t.Fatalf("%s %s", out, stderr)
				}
			case "help":
				if stderr != "" || !strings.Contains(out, "receipt") || !strings.Contains(out, "verify") {
					t.Fatalf("%s %s", out, stderr)
				}
			default:
				var report main.ExportVerification
				if stderr != "" || json.Unmarshal([]byte(out), &report) != nil || report.Verified != (mode != "mismatch") || !report.Checks.BytesWritten || !report.Checks.ItemsEmitted {
					t.Fatalf("out=%s err=%s", out, stderr)
				}
			}
		})
	}
}

func TestExportVerificationCLIInvalidArgumentsAreOffline(t *testing.T) {
	for _, args := range [][]string{
		{"export"}, {"export", "unknown"}, {"export", "verify"}, {"export", "verify", "file"},
		{"export", "verify", "-", "--receipt", "receipt"}, {"export", "verify", "file", "--receipt", "-"},
		{"export", "verify", "file", "extra", "--receipt", "receipt"}, {"export", "verify", "file", "--receipt="},
		{"export", "verify", "--unknown"},
	} {
		t.Run(strings.Join(args, "/"), func(t *testing.T) {
			calls := setupExportVerificationCLI(t)
			code, out, stderr := runExportVerificationCLI(t, append([]string{"--json"}, args...))
			if code != 1 || out != "" || stderr == "" || calls.Load() != 0 {
				t.Fatalf("exit=%d calls=%d out=%s err=%s", code, calls.Load(), out, stderr)
			}
		})
	}
}

func TestExportVerificationAcceptsActualListReceipts(t *testing.T) {
	for _, kind := range []string{"build", "invocation"} {
		t.Run(kind, func(t *testing.T) {
			setupExportVerificationCLI(t)
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				key, path := "items", "/v1/builds"
				if kind == "invocation" {
					key, path = "invocations", "/v1/invocations"
				}
				if r.Method != "GET" || r.URL.Path != path {
					t.Errorf("request=%s %s", r.Method, r.URL)
					http.NotFound(w, r)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{key: []map[string]string{{"id": "first"}, {"id": "second"}}})
			}))
			t.Cleanup(server.Close)
			t.Setenv("FAAS_API", server.URL)
			t.Setenv("FAAS_TOKEN", main.TestAPIKey('a'))
			path, receipt := filepath.Join(t.TempDir(), "rows.jsonl"), filepath.Join(t.TempDir(), "receipt.json")
			code, out, stderr := runExportVerificationCLI(t, []string{"--json", kind, "list", "--all", "--output", path})
			if code != 0 || stderr != "" || calls.Load() != 1 {
				t.Fatalf("exit=%d calls=%d out=%s err=%s", code, calls.Load(), out, stderr)
			}
			if err := os.WriteFile(receipt, []byte(out), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("FAAS_TOKEN", "")
			code, out, stderr = runExportVerificationCLI(t, []string{"--json", "export", "verify", "--receipt", receipt, "--", path})
			var result main.ExportVerification
			if code != 0 || stderr != "" || calls.Load() != 1 || json.Unmarshal([]byte(out), &result) != nil || !result.Verified || result.Observed.ItemsEmitted != 2 {
				t.Fatalf("exit=%d calls=%d out=%s err=%s", code, calls.Load(), out, stderr)
			}
		})
	}
}

// Keep a direct public entry-point invocation visible to the JSON parity gate.
// The matrix above separately checks the same path through its output helper.
func TestExportVerificationJSONDispatcher(t *testing.T) {
	calls := setupExportVerificationCLI(t)
	data := "{\"value\":9007199254740993}\n"
	path, receipt := filepath.Join(t.TempDir(), "rows.jsonl"), filepath.Join(t.TempDir(), "receipt.json")
	for name, contents := range map[string]string{path: data, receipt: integrityReceiptJSON(data, 1)} {
		if err := os.WriteFile(name, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	out, stderr, restore := main.SwapIO(t)
	defer restore()
	code := main.Run([]string{"--json", "export", "verify", path, "--receipt", receipt})
	var report main.ExportVerification
	if code != 0 || calls.Load() != 0 || stderr() != "" || json.Unmarshal(out.Bytes(), &report) != nil || !report.Verified || !report.Checks.SHA256 || !report.Checks.BytesWritten || !report.Checks.ItemsEmitted {
		t.Fatalf("exit=%d calls=%d out=%s err=%s", code, calls.Load(), out.String(), stderr())
	}
}
