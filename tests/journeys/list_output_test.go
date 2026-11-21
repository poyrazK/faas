package journeys

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// GA row 156: a complete private export, API failure, SIGINT and a racing file.
func TestListFileExportIsolatedCLI(t *testing.T) {
	cli := os.Getenv("GREGALE_JOURNEY_TEST_CLI")
	if tgt != nil {
		cli = tgt.cli
	}
	if cli == "" {
		t.Skip("set GREGALE_JOURNEY_TEST_CLI or configure a journey target")
	}
	for _, kind := range []string{"invocation", "build"} {
		for _, mode := range []string{"complete", "api-error", "interrupt", "destination-race"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				dir := t.TempDir()
				path := filepath.Join(dir, "rows.jsonl")
				opened := make(chan struct{})
				release := make(chan struct{})
				var openedOnce, releaseOnce sync.Once
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					n := calls.Add(1)
					key := "invocations"
					wantPath := "/v1/invocations"
					if kind == "build" {
						key = "items"
						wantPath = "/v1/builds"
					}
					if r.Method != "GET" || r.URL.Path != wantPath {
						t.Errorf("unexpected request: %s %s", r.Method, r.URL)
						http.NotFound(w, r)
						return
					}
					if n == 1 {
						_ = json.NewEncoder(w).Encode(map[string]any{key: []map[string]string{{"id": "first"}}, "next_before": "next"})
						return
					}
					openedOnce.Do(func() { close(opened) })
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
					if mode == "api-error" {
						w.Header().Set("Content-Type", "application/problem+json")
						w.Header().Set("X-Faas-Request-Id", "019a767e7f0070008000000000000001")
						w.WriteHeader(503)
						_, _ = io.WriteString(w, `{"status":503,"code":"service_unavailable","title":"Try later"}`)
						return
					}
					_ = json.NewEncoder(w).Encode(map[string]any{key: []map[string]string{{"id": "last"}}})
				}))
				t.Cleanup(server.Close)
				tg := &target{api: server.URL, cli: cli, home: t.TempDir(), appsDomain: "apps.example.test"}
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				cmd := exec.CommandContext(ctx, cli, "--non-interactive", "--json", kind, "list", "--all", "--limit", "1", "--output", path)
				cmd.Env, cmd.Dir = tg.cliEnv("fp_test_list-output"), tg.home
				var stdout, stderr bytes.Buffer
				cmd.Stdout = &stdout
				cmd.Stderr = &stderr
				if err := cmd.Start(); err != nil {
					cancel()
					t.Fatal(err)
				}
				done := make(chan struct{})
				var waitErr error
				go func() { waitErr = cmd.Wait(); close(done) }()
				defer func() { releaseOnce.Do(func() { close(release) }); cancel(); <-done }()
				select {
				case <-opened:
				case <-done:
					t.Fatalf("export stopped early: %v %s", waitErr, stderr.String())
				case <-time.After(5 * time.Second):
					t.Fatal("second page did not start")
				}
				if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("destination appeared before completion: %v", err)
				}
				entries, err := os.ReadDir(dir)
				if err != nil || len(entries) != 1 {
					t.Fatalf("private export missing: %v %v", entries, err)
				}
				info, err := entries[0].Info()
				if err != nil || info.Mode().Perm() != 0600 {
					t.Fatalf("private mode: %v %v", info, err)
				}
				if mode == "interrupt" {
					if err := cmd.Process.Signal(os.Interrupt); err != nil {
						t.Fatal(err)
					}
				} else {
					if mode == "destination-race" {
						if err := os.WriteFile(path, []byte("keep"), 0600); err != nil {
							t.Fatal(err)
						}
					}
					releaseOnce.Do(func() { close(release) })
				}
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Fatal("export did not stop")
				}
				code := 0
				if waitErr != nil {
					var exit *exec.ExitError
					if !errors.As(waitErr, &exit) {
						t.Fatal(waitErr)
					}
					code = exit.ExitCode()
				}
				if calls.Load() != 2 {
					t.Fatalf("unexpected requests: %d", calls.Load())
				}
				if mode == "complete" {
					var receipt struct {
						Published            bool
						Format, Path, SHA256 string
						Pages                int   `json:"pages_completed"`
						Items                int   `json:"items_emitted"`
						Bytes                int64 `json:"bytes_written"`
					}
					data, err := os.ReadFile(path)
					digest := sha256.Sum256(data)
					decoder := json.NewDecoder(&stdout)
					if code != 0 || stderr.Len() != 0 || err != nil || decoder.Decode(&receipt) != nil || decoder.Decode(new(any)) != io.EOF || !receipt.Published || receipt.Format != "jsonl" || receipt.Path != path || receipt.Items != 2 || receipt.Pages != 2 || receipt.Bytes != int64(len(data)) || receipt.SHA256 != hex.EncodeToString(digest[:]) {
						t.Fatalf("receipt: exit=%d stdout=%s stderr=%s data=%q err=%v", code, stdout.String(), stderr.String(), data, err)
					}
					retry := tg.exec(t, "fp_test_list-output", tg.home, time.Minute, kind, "list", "--all", "--output", path)
					after, _ := os.ReadFile(path)
					if retry.exit != 1 || calls.Load() != 2 || !bytes.Equal(data, after) {
						t.Fatal("retry overwrote or repeated the export")
					}
				} else {
					want := 1
					if mode == "api-error" {
						want = 3
					}
					if mode == "interrupt" {
						want = 130
					}
					var problem map[string]json.RawMessage
					if code != want || stdout.Len() != 0 || json.Unmarshal(stderr.Bytes(), &problem) != nil || problem["pagination"] != nil {
						t.Fatalf("failure: exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
					}
					if mode == "api-error" && (string(problem["code"]) != `"service_unavailable"` || string(problem["request_id"]) != `"019a767e7f0070008000000000000001"`) {
						t.Fatal(stderr.String())
					}
					if mode == "destination-race" {
						data, _ := os.ReadFile(path)
						if string(data) != "keep" {
							t.Fatal("racing file changed")
						}
					} else if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("failed destination exists: %v", err)
					}
				}
				entries, _ = os.ReadDir(dir)
				for _, entry := range entries {
					if entry.Name() != filepath.Base(path) {
						t.Fatalf("private partial retained: %s", entry.Name())
					}
				}
			})
		}
	}
}
