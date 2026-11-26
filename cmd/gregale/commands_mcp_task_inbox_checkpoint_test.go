package main_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofrs/flock"
	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

func TestMCPTaskInboxCheckpointAfterCompleteOutput(t *testing.T) {
	t.Setenv("GREGALE_TEST_INBOX_TOKEN", "original-watch-token")
	checkpoint := filepath.Join(t.TempDir(), "inbox.json")
	const (
		complete = iota
		partial
		outputFailure
	)
	var phase, pages atomic.Int32
	cursor := "YQ." + strings.Repeat("A", 43)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int
			Method string
			Params struct {
				Cursor string
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch request.Method {
		case "server/discover":
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"supportedVersions":[%q],"capabilities":{"tools":{},"extensions":{"%s":{},"io.gregale/task-inbox":{}}}}}`,
				request.ID, mcphosting.ProtocolVersion, mcphosting.TasksExtensionID)
		case "gregale/tasks/inbox":
			if pages.Add(1) != 1 {
				// End the watch at the second request. In the partial
				// phase this interrupts page two of the first scan.
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if request.Params.Cursor != "" {
				t.Error("watch did not start at the first page")
			}
			id, next := "previous-task", ""
			switch phase.Load() {
			case partial:
				id, next = "uncommitted-task", cursor
			case outputFailure:
				id = "unwritten-task"
			}
			const stamp = "2026-11-21T12:00:00Z"
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"resultType":"complete","tasks":[{"resultType":"complete","taskId":%q,"status":"input_required","createdAt":%q,"lastUpdatedAt":%q,"ttlMs":60000}],"nextCursor":%q}}`,
				request.ID, id, stamp, stamp, next)
		default:
			t.Errorf("unexpected watch method %q", request.Method)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	t.Cleanup(server.Close)

	run := func(mode int32, output io.Writer, args ...string) string {
		t.Helper()
		phase.Store(mode)
		pages.Store(0)
		oldOut := *main.OsStdout
		*main.OsStdout = output
		defer func() { *main.OsStdout = oldOut }()
		stderr, restore := main.CaptureStderr(t)
		defer restore()
		command := []string{"mcp", "client", "inbox", "--url", server.URL + "/mcp", "--token-env", "GREGALE_TEST_INBOX_TOKEN",
			"--checkpoint-file", checkpoint, "--limit", "1", "--status", "input_required"}
		if code := main.Run(append(command, args...)); code != 1 {
			t.Fatalf("watch exit = %d, stderr = %s", code, stderr)
		}
		return stderr.String()
	}
	readCheckpoint := func() []byte {
		t.Helper()
		body, err := os.ReadFile(checkpoint)
		if err != nil {
			t.Fatal(err)
		}
		return body
	}
	watchArgs := []string{"--watch", "--interval", "1s"}
	var output bytes.Buffer
	if err := run(complete, &output); !strings.Contains(err, "--checkpoint-file requires") || pages.Load() != 0 {
		t.Fatalf("checkpoint was accepted without --watch: %s", err)
	}
	if stderr := run(complete, &output, watchArgs...); !strings.Contains(output.String(), "previous-task") {
		t.Fatalf("initial watch did not publish the visible Task: %s", stderr)
	}
	before := readCheckpoint()

	output.Reset()
	run(partial, &output, watchArgs...)
	if pages.Load() != 2 || output.Len() != 0 || !bytes.Equal(before, readCheckpoint()) {
		t.Fatal("an incomplete page scan changed output or the checkpoint")
	}
	if err := run(outputFailure, failingInboxOutput{}, watchArgs...); !strings.Contains(err, io.ErrClosedPipe.Error()) {
		t.Fatalf("watch did not report failed output: %s", err)
	}
	if pages.Load() != 1 || !bytes.Equal(before, readCheckpoint()) {
		t.Fatal("a write failure advanced the checkpoint")
	}

	lock := flock.New(checkpoint + ".lock")
	ok, err := lock.TryLock()
	if err != nil || !ok {
		t.Fatalf("cannot take checkpoint lock: %v", err)
	}
	t.Cleanup(func() { _ = lock.Close() })
	if err := run(complete, &output, watchArgs...); !strings.Contains(err, "lock inbox checkpoint") || pages.Load() != 0 {
		t.Fatalf("watch ignored an active checkpoint lock: %s", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	if err := run(complete, &output, "--watch", "--status", "completed"); !strings.Contains(err, "another endpoint or status filter") || pages.Load() != 0 {
		t.Fatalf("watch ignored the saved status filter: %s", err)
	}

	t.Setenv("GREGALE_TEST_INBOX_TOKEN", "renewed-watch-token")
	run(complete, &output, watchArgs...)
	if !strings.Contains(output.String(), "previous-task") || bytes.Equal(before, readCheckpoint()) {
		t.Fatal("a renewed credential did not start a fresh history")
	}

	malformed := append(readCheckpoint(), []byte(` {}`)...)
	if err := os.WriteFile(checkpoint, malformed, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run(complete, &output, watchArgs...); !strings.Contains(err, "invalid inbox checkpoint") || pages.Load() != 0 ||
		!bytes.Equal(malformed, readCheckpoint()) {
		t.Fatalf("watch used or replaced an invalid checkpoint: %s", err)
	}
}

type failingInboxOutput struct{}

func (failingInboxOutput) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestMCPTaskInboxOnceFlagValidation(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--once"}, "--once requires --watch"},
		{[]string{"--watch", "--wait-timeout", "2s"}, "--wait-timeout requires"},
		{[]string{"--watch", "--once", "--wait-timeout", "0s"}, "requires a positive duration"},
		{[]string{"--watch", "--once", "--wait-timeout", "-1s"}, "requires a positive duration"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			stderr, restore := main.CaptureStderr(t)
			defer restore()
			if code := main.Run(append([]string{"mcp", "client", "inbox"}, tc.args...)); code != 1 ||
				!strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("invalid flags returned %d, stderr = %s", code, stderr)
			}
		})
	}
}

func TestMCPTaskInboxOnceWaitBudget(t *testing.T) {
	t.Setenv("GREGALE_TEST_INBOX_TOKEN", "once-watch-token")
	const stamp = "2026-11-21T12:00:00Z"
	cursor := "YQ." + strings.Repeat("A", 43)
	for _, tc := range []struct {
		name, budget string
		exit         int
		requests     int32
	}{
		{"event after empty scan", "5s", 0, 2},
		{"deadline during retry after", "2s", 124, 1},
		{"deadline during second page", "2s", 124, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					ID     int
					Method string
					Params struct {
						Cursor string
					}
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if request.Method == "server/discover" {
					_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"supportedVersions":[%q],"capabilities":{"tools":{},"extensions":{"%s":{},"io.gregale/task-inbox":{}}}}}`,
						request.ID, mcphosting.ProtocolVersion, mcphosting.TasksExtensionID)
					return
				}
				if request.Method != "gregale/tasks/inbox" {
					t.Errorf("unexpected method %q", request.Method)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				n := requests.Add(1)
				next := ""
				switch tc.name {
				case "event after empty scan":
					if n == 1 {
						_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"resultType":"complete","tasks":[]}}`, request.ID)
						return
					}
				case "deadline during retry after":
					w.Header().Set("Retry-After", "30")
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				case "deadline during second page":
					if n == 1 {
						next = cursor
					} else {
						if request.Params.Cursor != cursor {
							t.Error("the scan did not request the second page")
						}
						select {
						case <-r.Context().Done():
						case <-time.After(6 * time.Second):
							t.Error("the wait budget did not cancel the page request")
						}
						return
					}
				}
				_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"resultType":"complete","tasks":[{"resultType":"complete","taskId":"pending-task","status":"input_required","createdAt":%q,"lastUpdatedAt":%q,"ttlMs":60000}],"nextCursor":%q}}`,
					request.ID, stamp, stamp, next)
			}))
			t.Cleanup(server.Close)
			checkpoint := filepath.Join(t.TempDir(), "once.json")
			stdout, restoreOut := main.CaptureStdout(t)
			defer restoreOut()
			stderr, restoreErr := main.CaptureStderr(t)
			defer restoreErr()
			start := time.Now()
			code := main.Run([]string{"mcp", "client", "inbox", "--url", server.URL + "/mcp", "--token-env", "GREGALE_TEST_INBOX_TOKEN",
				"--watch", "--once", "--wait-timeout", tc.budget, "--timeout", "10s", "--interval", "1s", "--limit", "1",
				"--status", "input_required", "--checkpoint-file", checkpoint})
			if code != tc.exit || requests.Load() != tc.requests {
				t.Fatalf("exit = %d, inbox requests = %d, stdout = %s, stderr = %s", code, requests.Load(), stdout, stderr)
			}
			if tc.exit == 0 {
				if !strings.Contains(stdout.String(), `"taskId":"pending-task"`) || strings.Count(stdout.String(), "\n") != 1 {
					t.Fatalf("one-shot watch output = %s", stdout)
				}
				return
			}
			if stdout.String() != "" || !strings.Contains(stderr.String(), "--wait-timeout elapsed") || time.Since(start) >= 5*time.Second {
				t.Fatalf("wait deadline did not stop with empty stdout, stdout = %s, stderr = %s", stdout, stderr)
			}
			if _, err := os.Stat(checkpoint); !os.IsNotExist(err) {
				t.Fatalf("unsuccessful scan wrote a checkpoint: %v", err)
			}
		})
	}
}
