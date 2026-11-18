package journeys

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// GA row 156: progress arrives while an export is still private, then reports
// publication or stopping before the original completion receipt/Problem.
func TestListExportProgressIsolatedCLI(t *testing.T) {
	cli := os.Getenv("GREGALE_JOURNEY_TEST_CLI")
	if tgt != nil {
		cli = tgt.cli
	}
	if cli == "" {
		t.Skip("set GREGALE_JOURNEY_TEST_CLI or configure a journey target")
	}
	for _, kind := range []string{"build", "invocation"} {
		for _, mode := range []string{"complete", "api-error", "timeout", "interrupt"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				opened, release := make(chan struct{}), make(chan struct{})
				var once sync.Once
				var calls atomic.Int32
				const requestID = "019a767e7f0070008000000000000001"
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					n := calls.Add(1)
					key, want := "items", "/v1/builds"
					if kind == "invocation" {
						key, want = "invocations", "/v1/invocations"
					}
					if r.Method != "GET" || r.URL.Path != want {
						t.Errorf("unexpected request: %s %s", r.Method, r.URL)
						http.NotFound(w, r)
						return
					}
					if n == 1 {
						_ = json.NewEncoder(w).Encode(map[string]any{key: []map[string]string{{"id": "private-first"}}, "next_before": "private-cursor"})
						return
					}
					once.Do(func() { close(opened) })
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
					if mode == "api-error" {
						w.Header().Set("Content-Type", "application/problem+json")
						w.Header().Set("X-Faas-Request-Id", requestID)
						w.WriteHeader(503)
						_, _ = io.WriteString(w, `{"status":503,"code":"service_unavailable","title":"Try later"}`)
						return
					}
					_ = json.NewEncoder(w).Encode(map[string]any{key: []map[string]string{{"id": "private-last"}}})
				}))
				t.Cleanup(server.Close)
				tg := &target{api: server.URL, cli: cli, home: t.TempDir(), appsDomain: "apps.example.test"}
				dir := t.TempDir()
				path := filepath.Join(dir, "private-output.jsonl")
				args := []string{"--non-interactive", "--json", kind, "list", "--all", "--output", path, "--progress", "ndjson"}
				if mode == "timeout" {
					args = append(args, "--timeout", "1s")
				}
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, cli, args...)
				cmd.Env = tg.cliEnv("fp_test_private-export-progress")
				cmd.Dir = tg.home
				var stdout bytes.Buffer
				cmd.Stdout = &stdout
				stderr, err := cmd.StderrPipe()
				if err != nil {
					t.Fatal(err)
				}
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				first := make(chan struct{})
				var firstOnce sync.Once
				readDone := make(chan struct{})
				var raw []json.RawMessage
				var readErr error
				go func() {
					defer close(readDone)
					scanner := bufio.NewScanner(stderr)
					for scanner.Scan() {
						line := append(json.RawMessage(nil), scanner.Bytes()...)
						raw = append(raw, line)
						var event struct{ Type, Event string }
						if json.Unmarshal(line, &event) == nil && event.Type == "list_export_progress" && event.Event == "start" {
							firstOnce.Do(func() { close(first) })
						}
					}
					readErr = scanner.Err()
				}()
				done := make(chan struct{})
				var waitErr error
				go func() { <-readDone; waitErr = cmd.Wait(); close(done) }()
				defer func() { cancel(); <-done; <-readDone }()
				select {
				case <-first:
				case <-done:
					t.Fatal("no live progress before export ended")
				case <-ctx.Done():
					t.Fatal("progress did not start")
				}
				select {
				case <-opened:
				case <-done:
					t.Fatal("export stopped before second read")
				case <-ctx.Done():
					t.Fatal("second read did not start")
				}
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Fatalf("published while still reading: %v", err)
				}
				if mode == "interrupt" {
					if err := cmd.Process.Signal(os.Interrupt); err != nil {
						t.Fatal(err)
					}
				} else if mode != "timeout" {
					close(release)
				}
				select {
				case <-done:
				case <-ctx.Done():
					t.Fatal("export did not stop")
				}
				<-readDone
				if readErr != nil {
					t.Fatal(readErr)
				}
				code := 0
				if waitErr != nil {
					var exit *exec.ExitError
					if !errors.As(waitErr, &exit) {
						t.Fatal(waitErr)
					}
					code = exit.ExitCode()
				}
				want := 0
				if mode == "api-error" || mode == "timeout" {
					want = 3
				}
				if mode == "interrupt" {
					want = 130
				}
				var events []struct {
					Type, Event string
					Schema      int   `json:"schema_version"`
					Pages       int   `json:"pages_completed"`
					Items       int   `json:"items_emitted"`
					Bytes       int64 `json:"bytes_written"`
					Published   bool
				}
				var problems []map[string]json.RawMessage
				lastEventIndex, lastProblemIndex := -1, -1
				for i, line := range raw {
					var object map[string]json.RawMessage
					if json.Unmarshal(line, &object) != nil {
						t.Fatalf("bad NDJSON: %s", line)
					}
					if string(object["type"]) == `"list_export_progress"` {
						var event struct {
							Type, Event string
							Schema      int   `json:"schema_version"`
							Pages       int   `json:"pages_completed"`
							Items       int   `json:"items_emitted"`
							Bytes       int64 `json:"bytes_written"`
							Published   bool
						}
						_ = json.Unmarshal(line, &event)
						if len(object) != 8 || event.Schema != 1 || strings.Contains(string(line), "private") || strings.Contains(string(line), server.URL) {
							t.Fatalf("unsafe progress: %s", line)
						}
						events = append(events, event)
						lastEventIndex = i
					} else {
						problems = append(problems, object)
						lastProblemIndex = i
					}
				}
				if code != want || len(events) < 2 || events[0].Event != "start" || calls.Load() != 2 {
					t.Fatalf("export result: exit=%d events=%+v problems=%v calls=%d", code, events, problems, calls.Load())
				}
				last := events[len(events)-1]
				if mode == "complete" {
					data, err := os.ReadFile(path)
					var receipt struct {
						Published bool
						Pages     int   `json:"pages_completed"`
						Items     int   `json:"items_emitted"`
						Bytes     int64 `json:"bytes_written"`
					}
					decoder := json.NewDecoder(&stdout)
					if err != nil || decoder.Decode(&receipt) != nil || decoder.Decode(new(any)) != io.EOF || !receipt.Published || last.Event != "published" || !last.Published || last.Bytes != int64(len(data)) || last.Pages != receipt.Pages || last.Items != receipt.Items || len(problems) != 0 {
						t.Fatalf("publication/receipt mismatch: %+v %+v %v", last, receipt, err)
					}
				} else {
					if stdout.Len() != 0 || len(problems) != 1 || last.Event != "stopped" || last.Published || last.Pages != 1 || last.Items != 1 || last.Bytes == 0 || lastEventIndex >= lastProblemIndex || string(problems[0]["exit_code"]) != map[int]string{3: "3", 130: "130"}[want] || problems[0]["pagination"] != nil || problems[0]["cli"] != nil {
						t.Fatalf("stopped/error mismatch: %+v %v stdout=%s", last, problems, stdout.String())
					}
					if mode == "api-error" && (string(problems[0]["code"]) != `"service_unavailable"` || string(problems[0]["request_id"]) != `"`+requestID+`"`) {
						t.Fatal(problems)
					}
					if _, err := os.Lstat(path); !os.IsNotExist(err) {
						t.Fatalf("failed export published: %v", err)
					}
				}
			})
		}
	}
}
