package main_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestInvokeAsyncWaitTerminalResults(t *testing.T) {
	for _, state := range []string{"completed", "failed", "cancelled", "dead_letter"} {
		for _, machine := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/json=%v", state, machine), func(t *testing.T) {
				main.ResetJSONOut(t)
				var posts, gets atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					switch r.Method + " " + r.URL.Path {
					case "POST /v1/apps/api/invoke/async":
						posts.Add(1)
						var req api.InvokeRequest
						if json.NewDecoder(r.Body).Decode(&req) != nil || string(req.Payload) != `{"n":9007199254740993}` || req.Method != "PUT" || req.Path != "/work" {
							t.Errorf("submission changed payload or routing: %+v", req)
						}
						w.WriteHeader(http.StatusAccepted)
						_ = json.NewEncoder(w).Encode(api.AsyncInvokeResponse{ID: "inv-accepted", StatusURL: "https://untrusted.invalid/never-follow"})
					case "GET /v1/invocations/inv-accepted":
						inv := api.Invocation{ID: "inv-accepted", State: state, Result: json.RawMessage(`{"ok":true}`), LastError: "handler detail"}
						if gets.Add(1) == 1 {
							inv.State, inv.Result, inv.LastError = "pending", nil, ""
						}
						_ = json.NewEncoder(w).Encode(inv)
					default:
						t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
						http.NotFound(w, r)
					}
				}))
				defer server.Close()
				t.Setenv("FAAS_API", server.URL)
				t.Setenv("FAAS_TOKEN", "test-token")
				out, stderr, restore := main.SwapIO(t)
				defer restore()
				args := []string{"invocation", "run", "api", "--wait", "--interval", "1ms", "--timeout", "2s", "--payload", `{"n":9007199254740993}`, "--method", "PUT", "--path", "/work"}
				if machine {
					args = append(args, "--json")
				}
				want := 1
				if state == "completed" {
					want = 0
				}
				if code := main.Run(args); code != want || posts.Load() != 1 || gets.Load() != 2 {
					t.Fatalf("exit=%d posts=%d gets=%d stderr=%s", code, posts.Load(), gets.Load(), stderr())
				}
				if machine {
					var got api.Invocation
					var result struct{ OK bool }
					if json.Unmarshal(out.Bytes(), &got) != nil || got.ID != "inv-accepted" || got.State != state || json.Unmarshal(got.Result, &result) != nil || !result.OK {
						t.Fatalf("expected one final JSON record: %s", out.String())
					}
				} else if !strings.Contains(out.String(), state) || !strings.Contains(out.String(), "inv-accepted") {
					t.Fatalf("missing final result: %s", out.String())
				}
			})
		}
	}
}

func TestInvokeAsyncWaitInvalidOptionsDoNotSubmit(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	for _, args := range [][]string{
		{"--async=false", "--wait"}, {"--wait", "--async=false"}, {"--timeout", "0"}, {"--async", "--timeout", "1s"},
		{"--async", "--interval", "1s"}, {"--async", "--wait=false", "--timeout", "0"},
		{"--wait", "--timeout", "-1s"}, {"--wait", "--interval", "0"},
		{"--async", "--wait", "--interval", "-1s"}, {"--async", "--wait", "--timeout", "invalid"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			main.ResetJSONOut(t)
			_, _, restore := main.SwapIO(t)
			defer restore()
			if code := main.Run(append([]string{"invoke", "api"}, args...)); code != 1 || calls.Load() != 0 {
				t.Fatalf("exit=%d requests=%d", code, calls.Load())
			}
		})
	}
}

func TestInvokeAsyncWaitTimeoutPreservesAcceptance(t *testing.T) {
	for _, haveSnapshot := range []bool{false, true} {
		t.Run(fmt.Sprint(haveSnapshot), func(t *testing.T) {
			main.ResetJSONOut(t)
			var posts, gets atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					posts.Add(1)
					_ = json.NewEncoder(w).Encode(api.AsyncInvokeResponse{ID: "inv-timeout"})
					return
				}
				gets.Add(1)
				if haveSnapshot {
					_ = json.NewEncoder(w).Encode(api.Invocation{ID: "inv-timeout", State: "dispatching"})
				} else {
					<-r.Context().Done()
				}
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			t.Setenv("FAAS_TOKEN", "test-token")
			out, stderr, restore := main.SwapIO(t)
			defer restore()
			if code := main.Run([]string{"invocation", "run", "api", "--wait", "--json", "--interval", "1h", "--timeout", "100ms"}); code != 124 || posts.Load() != 1 || gets.Load() != 1 {
				t.Fatalf("exit=%d posts=%d gets=%d stderr=%s", code, posts.Load(), gets.Load(), stderr())
			}
			assertInvokeResumeProblem(t, stderr(), "inv-timeout", "invocation_wait_timeout", 124)
			if haveSnapshot {
				var got api.Invocation
				if json.Unmarshal(out.Bytes(), &got) != nil || got.ID != "inv-timeout" || got.State != "dispatching" {
					t.Fatalf("last known status: %s", out.String())
				}
			} else if out.Len() != 0 {
				t.Fatalf("invented status before first poll: %s", out.String())
			}
		})
	}
}

func TestInvokeAsyncWaitPollFailureNeverResubmits(t *testing.T) {
	for _, status := range []int{401, 403, 404, 429, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			main.ResetJSONOut(t)
			var posts, gets atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					posts.Add(1)
					_ = json.NewEncoder(w).Encode(api.AsyncInvokeResponse{ID: "inv-failure"})
					return
				}
				gets.Add(1)
				w.Header().Set("Content-Type", "application/problem+json")
				w.WriteHeader(status)
				_ = json.NewEncoder(w).Encode(api.Problem{Status: status, Code: "poll_failed", Title: "Cannot inspect invocation"})
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			t.Setenv("FAAS_TOKEN", "test-token")
			out, stderr, restore := main.SwapIO(t)
			defer restore()
			want := 1
			if status == 401 {
				want = 2
			} else if status >= 500 {
				want = 3
			}
			if code := main.Run([]string{"invoke", "api", "--async", "--wait", "--json"}); code != want || posts.Load() != 1 || gets.Load() != 1 || out.Len() != 0 {
				t.Fatalf("exit=%d posts=%d gets=%d stdout=%s stderr=%s", code, posts.Load(), gets.Load(), out.String(), stderr())
			}
			assertInvokeResumeProblem(t, stderr(), "inv-failure", "poll_failed", want)
		})
	}
}

func TestInvokeAsyncWaitRejectsInvalidReceiptAndWrongStatusID(t *testing.T) {
	for _, id := range []string{"", "..", "../another", "inv\nescape", "inv-valid"} {
		t.Run(fmt.Sprintf("id=%q", id), func(t *testing.T) {
			main.ResetJSONOut(t)
			var posts, gets atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					posts.Add(1)
					_ = json.NewEncoder(w).Encode(api.AsyncInvokeResponse{ID: id})
				} else {
					gets.Add(1)
					_ = json.NewEncoder(w).Encode(api.Invocation{ID: "another-invocation", State: "completed"})
				}
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			t.Setenv("FAAS_TOKEN", "test-token")
			out, stderr, restore := main.SwapIO(t)
			defer restore()
			if code := main.Run([]string{"invoke", "api", "--async", "--wait", "--json", "--timeout", "1s"}); code != 1 || posts.Load() != 1 || out.Len() != 0 {
				t.Fatalf("exit=%d posts=%d stdout=%s stderr=%s", code, posts.Load(), out.String(), stderr())
			}
			wantGets := int32(0)
			if id == "inv-valid" {
				wantGets = 1
				if !strings.Contains(stderr(), "invocation wait inv-valid") {
					t.Fatalf("missing recovery after mismatched response: %s", stderr())
				}
			}
			if gets.Load() != wantGets {
				t.Fatalf("GET count=%d want=%d", gets.Load(), wantGets)
			}
			if id == "inv-valid" {
				assertInvocationWaitRecovery(t, stderr(), id)
			} else {
				var problem invocationWaitDiagnostic
				if json.Unmarshal([]byte(stderr()), &problem) != nil || problem.Recovery != nil {
					t.Fatalf("invented recovery for invalid receipt: %s", stderr())
				}
			}
		})
	}
}

func assertInvokeResumeProblem(t *testing.T, stderr, id, code string, exit int) {
	t.Helper()
	got := assertInvocationWaitRecovery(t, stderr, id)
	if got.Code != code || got.ExitCode != exit ||
		!strings.Contains(got.Hint, "invocation wait "+id) || !strings.Contains(got.Hint, "--profile") {
		t.Fatalf("missing accepted ID or resume command: %+v: %s", got, stderr)
	}
}

// Keep duration parsing covered above without imposing the wait duration on
// submission: this server accepts after a longer delay than the local wait.
func TestInvokeAsyncWaitTimeoutStartsAfterAcceptance(t *testing.T) {
	main.ResetJSONOut(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			time.Sleep(400 * time.Millisecond)
			_ = json.NewEncoder(w).Encode(api.AsyncInvokeResponse{ID: "inv-slow-accept"})
		} else {
			_ = json.NewEncoder(w).Encode(api.Invocation{ID: "inv-slow-accept", State: "completed"})
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	_, stderr, restore := main.SwapIO(t)
	defer restore()
	if code := main.Run([]string{"invoke", "api", "--async", "--wait", "--timeout", "200ms"}); code != 0 {
		t.Fatalf("wait deadline incorrectly covered submission: exit=%d stderr=%s", code, stderr())
	}
}
