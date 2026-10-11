package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type restartPollTestClient struct {
	rows  []api.RuntimeConfigRestartStatusResponse
	err   error
	calls int
}

func (c *restartPollTestClient) GetRuntimeConfigRestartStatus(context.Context, string, string) (api.RuntimeConfigRestartStatusResponse, error) {
	c.calls++
	if c.err != nil {
		return api.RuntimeConfigRestartStatusResponse{}, c.err
	}
	index := c.calls - 1
	if index >= len(c.rows) {
		index = len(c.rows) - 1
	}
	return c.rows[index], nil
}

func restartStatusTestReceipt(status string) api.RuntimeConfigRestartStatusResponse {
	row := api.RuntimeConfigRestartStatusResponse{WakeID: uuid.NewString(), Status: status, Attempts: 1, RequestedAt: time.Now().UTC().Add(-time.Minute)}
	if status == "completed" {
		at := time.Now().UTC()
		row.CompletedAt = &at
	}
	return row
}

func TestPollAppRestartStatusRejectsUnconfirmedCompletion(t *testing.T) {
	for _, scenario := range []string{"completed", "failed", "wrong restart", "unknown state", "missing completion time", "missing request time", "negative attempts"} {
		t.Run(scenario, func(t *testing.T) {
			row := restartStatusTestReceipt("completed")
			wakeID := row.WakeID
			switch scenario {
			case "failed":
				row.Status = "failed"
			case "wrong restart":
				row.WakeID = uuid.NewString()
			case "unknown state":
				row.Status = "done"
			case "missing completion time":
				row.CompletedAt = nil
			case "missing request time":
				row.RequestedAt = time.Time{}
			case "negative attempts":
				row.Attempts = -1
			}
			client := &restartPollTestClient{rows: []api.RuntimeConfigRestartStatusResponse{row}}
			last, err := pollAppRestartStatus(t.Context(), client, "demo", wakeID, true, time.Millisecond, nil)
			if scenario == "completed" {
				if err != nil || last.Status != "completed" {
					t.Fatalf("completion: %+v %v", last, err)
				}
			} else if err == nil {
				t.Fatal("invalid or failed receipt treated as success")
			}
		})
	}
}

func TestPollAppRestartStatusTimeoutAndCancellationKeepLastProgress(t *testing.T) {
	for _, cancelNow := range []bool{false, true} {
		t.Run(map[bool]string{false: "timeout", true: "cancelled"}[cancelNow], func(t *testing.T) {
			row := restartStatusTestReceipt("retrying")
			row.FailureReason = "requests_active"
			client := &restartPollTestClient{rows: []api.RuntimeConfigRestartStatusResponse{row}}
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
			defer cancel()
			update := func(api.RuntimeConfigRestartStatusResponse) {
				if cancelNow {
					cancel()
				}
			}
			last, err := pollAppRestartStatus(ctx, client, "demo", row.WakeID, true, time.Hour, update)
			if (!errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled)) || last.FailureReason != "requests_active" || client.calls != 1 {
				t.Fatalf("lost pending work on client stop: %+v %v calls=%d", last, err, client.calls)
			}
		})
	}
}

func configureRestartTest(t *testing.T, base string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	// macOS ignores XDG_CONFIG_HOME; HOME isolates os.UserConfigDir there.
	t.Setenv("HOME", os.Getenv("XDG_CONFIG_HOME"))
	t.Setenv("FAAS_TOKEN", "test")
	t.Setenv("FAAS_API", base)
	t.Cleanup(resetJSONOutput)
}

func TestCmdAppRestartFreshWaitPostsOnceAndReadsExactRequest(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		t.Run(map[bool]string{false: "human", true: "json"}[asJSON], func(t *testing.T) {
			row := restartStatusTestReceipt("retrying")
			row.FailureReason = "requests_active"
			posts, gets := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == "POST" && r.URL.Path == "/v1/apps/demo/restart" {
					posts++
					if r.URL.RawQuery != "fresh=true" {
						t.Errorf("wrong restart kind: %s", r.URL)
					}
					w.WriteHeader(http.StatusAccepted)
					_ = json.NewEncoder(w).Encode(api.AppRestartResponse{WakeID: row.WakeID})
				} else if r.Method == "GET" && r.URL.Path == "/v1/apps/demo/runtime-config-restarts/"+row.WakeID {
					gets++
					out := row
					if gets > 1 {
						out.Status = "completed"
						at := time.Now().UTC()
						out.CompletedAt = &at
					}
					_ = json.NewEncoder(w).Encode(out)
				} else {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
					http.Error(w, "unexpected", 500)
				}
			}))
			t.Cleanup(server.Close)
			configureRestartTest(t, server.URL)
			jsonOutput = false
			stdout, stderr, restore := swapIO(t)
			defer restore()
			args := []string{"demo", "restart", "--fresh", "--wait", "--poll-interval", "1ms", "--timeout", "1s"}
			if asJSON {
				args = append(args, "--json")
			}
			if code := run(append([]string{"app"}, args...)); code != 0 {
				t.Fatalf("wait exit=%d: %s", code, stderr())
			}
			if posts != 1 || gets != 2 {
				t.Fatalf("restart repeated or lost: POST=%d GET=%d", posts, gets)
			}
			if asJSON {
				var got api.RuntimeConfigRestartStatusResponse
				decoder := json.NewDecoder(stdout)
				if err := decoder.Decode(&got); err != nil || got.WakeID != row.WakeID || got.Status != "completed" {
					t.Fatalf("JSON receipt: %+v %v", got, err)
				}
				if err := decoder.Decode(&got); !errors.Is(err, io.EOF) {
					t.Fatal("stdout contains more than one JSON receipt")
				}
			} else if !strings.Contains(stderr(), "Waiting for active requests to finish") || !strings.Contains(stdout.String(), "Application health has not been verified") {
				t.Fatalf("missing explanation: %s %s", stdout, stderr())
			}
		})
	}
}

func TestCmdAppRestartStatusOnlyReadsAndPreservesTimeoutReceipt(t *testing.T) {
	t.Run("timeout", func(t *testing.T) { testRestartStatusReadOutcome(t, "retrying") })
	t.Run("failed", func(t *testing.T) { testRestartStatusReadOutcome(t, "failed") })
}

func testRestartStatusReadOutcome(t *testing.T, status string) {
	t.Helper()
	row := restartStatusTestReceipt(status)
	row.FailureReason = "requests_active"
	gets := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/apps/demo/runtime-config-restarts/"+row.WakeID {
			t.Errorf("status mutated app: %s %s", r.Method, r.URL)
		}
		gets++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(row)
	}))
	t.Cleanup(server.Close)
	configureRestartTest(t, server.URL)
	jsonOutput = true
	stdout, stderr, restore := swapIO(t)
	defer restore()
	code := cmdAppDispatch([]string{"demo", "restart", "status", "--wake-id", row.WakeID, "--wait", "--timeout", "100ms", "--poll-interval", "1h"})
	var got api.RuntimeConfigRestartStatusResponse
	resumeShown := strings.Contains(stderr(), "Resume following this request:")
	if code == 0 || gets != 1 || json.Unmarshal(stdout.Bytes(), &got) != nil || got.Status != status || got.WakeID != row.WakeID || resumeShown != (status == "retrying") {
		t.Fatalf("timeout lost receipt: exit=%d GET=%d %s %s", code, gets, stdout, stderr())
	}
}

func TestCmdAppRestartUnavailableStatusDoesNotResubmit(t *testing.T) {
	wakeID := uuid.NewString()
	posts, gets := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "POST" {
			posts++
			w.WriteHeader(202)
			_ = json.NewEncoder(w).Encode(api.AppRestartResponse{WakeID: wakeID})
		} else {
			gets++
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "restart status not available"))
		}
	}))
	t.Cleanup(server.Close)
	configureRestartTest(t, server.URL)
	jsonOutput = true
	stdout, stderr, restore := swapIO(t)
	defer restore()
	code := cmdAppRestart("demo", []string{"--fresh", "--wait", "--timeout", "1s"})
	var receipt api.AppRestartResponse
	if code == 0 || posts != 1 || gets != 1 || json.Unmarshal(stdout.Bytes(), &receipt) != nil || receipt.WakeID != wakeID || !strings.Contains(stderr(), restartStatusCommand("demo", wakeID)) {
		t.Fatalf("unavailable read lost accepted request: exit=%d POST=%d GET=%d %s %s", code, posts, gets, stdout, stderr())
	}
}

func TestCmdAppRestartSnapshotBehaviorAndArgumentValidation(t *testing.T) {
	posts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts++
		if r.Method != "POST" || r.URL.String() != "/v1/apps/demo/restart" {
			t.Errorf("snapshot behavior changed: %s %s", r.Method, r.URL)
		}
		_ = json.NewEncoder(w).Encode(api.AppRestartResponse{WakeID: uuid.NewString()})
	}))
	t.Cleanup(server.Close)
	configureRestartTest(t, server.URL)
	stdout, _, restore := swapIO(t)
	defer restore()
	if code := cmdAppRestart("demo", nil); code != 0 || !strings.Contains(stdout.String(), "Restart requested") {
		t.Fatalf("snapshot restart: %d %s", code, stdout)
	}
	for _, args := range [][]string{{"--wait"}, {"--fresh", "--timeout", "0s"}, {"--poll-interval", "0s"}, {"status"}, {"status", "--wake-id", "bad"}, {"status", "--wake-id", uuid.Nil.String()}, {"status", "--fresh"}, {"extra"}} {
		if code := cmdAppRestart("demo", args); code == 0 {
			t.Errorf("invalid arguments accepted: %v", args)
		}
	}
	if posts != 1 {
		t.Fatalf("invalid arguments submitted a restart: %d", posts)
	}
}
