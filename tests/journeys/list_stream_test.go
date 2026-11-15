package journeys

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// GA row 156: early rows, partial API failure and cancellation of a live read.
func TestListStreamingIsolatedCLI(t *testing.T) {
	cli := os.Getenv("GREGALE_JOURNEY_TEST_CLI")
	if tgt != nil {
		cli = tgt.cli
	}
	if cli == "" {
		t.Skip("set GREGALE_JOURNEY_TEST_CLI or configure a journey target")
	}
	for _, mode := range []string{"complete", "api-error", "interrupt"} {
		t.Run(mode, func(t *testing.T) {
			release := make(chan struct{})
			opened := make(chan struct{}, 1)
			closed := make(chan struct{}, 1)
			var releaseOnce sync.Once
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != "GET" || r.URL.Path != "/v1/invocations" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
					http.NotFound(w, r)
					return
				}
				if r.URL.Query().Get("before") == "" {
					_, _ = w.Write([]byte(`{"invocations":[{"id":"first"}],"next_before":"next"}`))
					return
				}
				opened <- struct{}{}
				if mode == "interrupt" {
					<-r.Context().Done()
					closed <- struct{}{}
					return
				}
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				if mode == "api-error" {
					w.Header().Set("Content-Type", "application/problem+json")
					w.Header().Set("X-Faas-Request-Id", "019a767e7f0070008000000000000001")
					w.WriteHeader(503)
					_, _ = w.Write([]byte(`{"status":503,"code":"service_unavailable","title":"Try later"}`))
					return
				}
				_, _ = w.Write([]byte(`{"invocations":[{"id":"second"}]}`))
			}))
			defer server.Close()
			tg := &target{api: server.URL, cli: cli, home: t.TempDir(), appsDomain: "apps.example.test"}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, cli, "--non-interactive", "--json", "invocation", "list", "--all", "--stream", "--limit", "1")
			cmd.Env, cmd.Dir = tg.cliEnv("fp_test_list-stream"), tg.home
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			pipe, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err = cmd.Start(); err != nil {
				t.Fatal(err)
			}
			first := make(chan string, 1)
			readDone := make(chan struct{})
			var lines []string
			var readErr error
			go func() {
				defer close(readDone)
				scan := bufio.NewScanner(pipe)
				for scan.Scan() {
					line := scan.Text()
					lines = append(lines, line)
					if len(lines) == 1 {
						first <- line
					}
				}
				readErr = scan.Err()
			}()
			var reap sync.Once
			var waitErr error
			wait := func() { reap.Do(func() { waitErr = cmd.Wait() }) }
			defer func() { releaseOnce.Do(func() { close(release) }); cancel(); <-readDone; wait() }()
			select {
			case line := <-first:
				var row struct{ ID string }
				if json.Unmarshal([]byte(line), &row) != nil || row.ID != "first" {
					t.Fatal(line)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("first row blocked behind next page")
			}
			select {
			case <-opened:
			case <-time.After(5 * time.Second):
				t.Fatal("next read did not start")
			}
			if mode == "interrupt" {
				if err := cmd.Process.Signal(os.Interrupt); err != nil {
					t.Fatal(err)
				}
			} else {
				releaseOnce.Do(func() { close(release) })
			}
			select {
			case <-readDone:
			case <-time.After(5 * time.Second):
				t.Fatal("stream did not stop")
			}
			wait()
			if readErr != nil {
				t.Fatal(readErr)
			}
			if calls.Load() != 2 {
				t.Fatalf("requests=%d", calls.Load())
			}
			if mode == "complete" {
				if waitErr != nil || len(lines) != 2 || stderr.Len() != 0 {
					t.Fatalf("err=%v lines=%q stderr=%s", waitErr, lines, stderr.String())
				}
				return
			}
			want := 3
			if mode == "interrupt" {
				want = 130
			}
			var exit *exec.ExitError
			var problem struct {
				Code       string
				ExitCode   int    `json:"exit_code"`
				RequestID  string `json:"request_id"`
				Pagination struct {
					PagesCompleted int `json:"pages_completed"`
					ItemsEmitted   int `json:"items_emitted"`
					Partial        bool
				}
			}
			if !errors.As(waitErr, &exit) || exit.ExitCode() != want || len(lines) != 1 || json.Unmarshal(stderr.Bytes(), &problem) != nil || problem.ExitCode != want || problem.Pagination.PagesCompleted != 1 || problem.Pagination.ItemsEmitted != 1 || !problem.Pagination.Partial {
				t.Fatalf("exit=%v lines=%q stderr=%s", waitErr, lines, stderr.String())
			}
			if mode == "api-error" && (problem.Code != "service_unavailable" || problem.RequestID != "019a767e7f0070008000000000000001") {
				t.Fatal(stderr.String())
			}
			if mode == "interrupt" {
				select {
				case <-closed:
				case <-time.After(time.Second):
					t.Fatal("active HTTP read survived interruption")
				}
			}
		})
	}
}
