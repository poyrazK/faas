package journeys

import (
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
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// GA row 156: one budget cancels a live paginated read without publishing a
// private export, while signal cancellation keeps its existing exit/progress.
func TestListOverallTimeoutIsolatedCLI(t *testing.T) {
	cli := os.Getenv("GREGALE_JOURNEY_TEST_CLI")
	if tgt != nil {
		cli = tgt.cli
	}
	if cli == "" {
		t.Skip("set GREGALE_JOURNEY_TEST_CLI or configure a journey target")
	}
	for _, kind := range []string{"build", "invocation"} {
		for _, mode := range []string{"all", "stream", "file", "interrupt-stream", "interrupt-file"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				file := mode == "file" || mode == "interrupt-file"
				stream := mode == "stream" || mode == "interrupt-stream"
				interrupt := mode == "interrupt-file" || mode == "interrupt-stream"
				var calls atomic.Int32
				opened, closed := make(chan struct{}), make(chan struct{})
				var openOnce, closeOnce sync.Once
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
						_ = json.NewEncoder(w).Encode(map[string]any{key: []map[string]string{{"id": "first"}}, "next_before": "next"})
						return
					}
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(200)
					_, _ = io.WriteString(w, `{"`+key+`":[`)
					w.(http.Flusher).Flush()
					openOnce.Do(func() { close(opened) })
					<-r.Context().Done()
					closeOnce.Do(func() { close(closed) })
				}))
				t.Cleanup(server.Close)
				tg := &target{api: server.URL, cli: cli, home: t.TempDir(), appsDomain: "apps.example.test"}
				path := filepath.Join(t.TempDir(), "rows.jsonl")
				budget := "1s"
				if interrupt {
					budget = "1m"
				}
				args := []string{kind, "list", "--all", "--timeout", budget}
				if stream {
					args = append(args, "--stream")
				}
				if file {
					args = append(args, "--output", path)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, cli, append([]string{"--non-interactive"}, args...)...)
				cmd.Env = tg.cliEnv("fp_test_list-timeout-journey")
				cmd.Dir = tg.home
				var stdout, stderr bytes.Buffer
				cmd.Stdout, cmd.Stderr = &stdout, &stderr
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				done := make(chan struct{})
				var waitErr error
				go func() { waitErr = cmd.Wait(); close(done) }()
				defer func() { cancel(); <-done }()
				select {
				case <-opened:
				case <-done:
					t.Fatalf("stopped before the live read: %v %s", waitErr, stderr.String())
				case <-ctx.Done():
					t.Fatal("second page did not start")
				}
				if file {
					if _, err := os.Lstat(path); !os.IsNotExist(err) {
						t.Fatalf("published before completion: %v", err)
					}
				}
				if interrupt {
					if err := cmd.Process.Signal(os.Interrupt); err != nil {
						t.Fatal(err)
					}
				}
				select {
				case <-done:
				case <-ctx.Done():
					t.Fatal("list did not finish within its read budget")
				}
				var exit *exec.ExitError
				if !errors.As(waitErr, &exit) {
					t.Fatalf("expected nonzero process exit: %v", waitErr)
				}
				wantExit := 3
				if interrupt {
					wantExit = 130
				}
				var problem struct {
					Code       string
					Exit       int `json:"exit_code"`
					CLI        json.RawMessage
					Pagination *struct {
						Pages   int `json:"pages_completed"`
						Items   int `json:"items_emitted"`
						Partial bool
					}
				}
				decoder := json.NewDecoder(&stderr)
				if exit.ExitCode() != wantExit || decoder.Decode(&problem) != nil || decoder.Decode(new(any)) != io.EOF || problem.Exit != wantExit || problem.Code != "transport_error" || problem.CLI != nil || calls.Load() != 2 {
					t.Fatalf("deadline/signal contract: exit=%d stdout=%s problem=%+v calls=%d", exit.ExitCode(), stdout.String(), problem, calls.Load())
				}
				select {
				case <-closed:
				case <-time.After(3 * time.Second):
					t.Fatal("process left the HTTP read active")
				}
				if stream {
					var row struct{ ID string }
					if json.Unmarshal(stdout.Bytes(), &row) != nil || row.ID != "first" || problem.Pagination == nil || !problem.Pagination.Partial || problem.Pagination.Pages != 1 || problem.Pagination.Items != 1 {
						t.Fatalf("partial stream: %s %+v", stdout.String(), problem)
					}
				} else if stdout.Len() != 0 || problem.Pagination != nil {
					t.Fatalf("private/buffered rows leaked: %s %+v", stdout.String(), problem)
				}
				if file {
					if _, err := os.Lstat(path); !os.IsNotExist(err) {
						t.Fatalf("deadline/signal published file: %v", err)
					}
				}
			})
		}
	}
}
