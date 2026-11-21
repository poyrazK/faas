package main_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

type invocationWaitDiagnostic struct {
	api.Problem
	Category string `json:"category"`
	ExitCode int    `json:"exit_code"`
	Recovery *struct {
		InvocationID   string `json:"invocation_id"`
		InspectCommand string `json:"inspect_command"`
		ResumeCommand  string `json:"resume_command"`
	} `json:"recovery"`
}

func assertInvocationWaitRecovery(t *testing.T, data, id string) invocationWaitDiagnostic {
	t.Helper()
	var got invocationWaitDiagnostic
	if err := json.Unmarshal([]byte(data), &got); err != nil || got.Recovery == nil || got.Recovery.InvocationID != id ||
		!strings.Contains(got.Recovery.InspectCommand, " invocation get "+id) ||
		!strings.Contains(got.Recovery.ResumeCommand, " invocation wait "+id) || got.Category == "" {
		t.Fatalf("missing structured recovery: %+v: %v: %s", got, err, data)
	}
	return got
}

func TestInvocationWaitRecoveryLocalPollingErrors(t *testing.T) {
	for _, submitted := range []bool{false, true} {
		for _, failure := range []string{"transport", "malformed", "mismatched"} {
			t.Run(fmt.Sprintf("%s/submitted=%t", failure, submitted), func(t *testing.T) {
				main.SetupConnectionProfiles(t)
				main.ResetJSONOut(t)
				var posts, gets atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Connection", "close")
					if r.Method == http.MethodPost {
						posts.Add(1)
						_ = json.NewEncoder(w).Encode(api.AsyncInvokeResponse{ID: "inv-recovery"})
						return
					}
					gets.Add(1)
					switch failure {
					case "transport":
						conn, _, err := w.(http.Hijacker).Hijack()
						if err != nil {
							t.Error(err)
							return
						}
						_ = conn.Close()
					case "malformed":
						_, _ = w.Write([]byte(`{"id":`))
					case "mismatched":
						_ = json.NewEncoder(w).Encode(api.Invocation{ID: "unrelated-record", State: "completed"})
					}
				}))
				defer server.Close()
				t.Setenv("FAAS_API", server.URL)
				t.Setenv("FAAS_TOKEN", "private-token")
				out, stderr, restore := main.SwapIO(t)
				defer restore()
				args := []string{"invocation", "wait", "inv-recovery"}
				wantPosts := int32(0)
				if submitted {
					args, wantPosts = []string{"invocation", "run", "api", "--wait"}, 1
				}
				wantExit, wantCode := 1, "invalid_request"
				if failure == "transport" {
					wantExit, wantCode = 3, "transport_error"
				}
				if code := main.Run(append([]string{"--profile", "staging", "--json"}, args...)); code != wantExit || posts.Load() != wantPosts || gets.Load() != 1 || out.Len() != 0 {
					t.Fatalf("exit=%d posts=%d gets=%d stdout=%s stderr=%s", code, posts.Load(), gets.Load(), out.String(), stderr())
				}
				got := assertInvocationWaitRecovery(t, stderr(), "inv-recovery")
				if got.ExitCode != wantExit || got.Code != wantCode || got.Recovery.ResumeCommand != "gregale --profile staging invocation wait inv-recovery" || strings.Contains(stderr(), "unrelated-record") {
					t.Fatalf("polling error changed classification or recovery target: %s", stderr())
				}
			})
		}
	}
}

func TestInvocationWaitRecoveryRedactsCredentials(t *testing.T) {
	main.SetupConnectionProfiles(t)
	main.ResetJSONOut(t)
	const secret = "fp_live_private"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			_ = json.NewEncoder(w).Encode(api.AsyncInvokeResponse{ID: secret})
			return
		}
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(api.Problem{Status: 403, Code: "scope_required", Detail: "credential " + secret, Hint: "Use a read-scoped credential " + secret})
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", secret)
	_, stderr, restore := main.SwapIO(t)
	defer restore()
	if code := main.Run([]string{"--profile", "staging", "--json", "invocation", "run", "api", "--wait"}); code != 1 {
		t.Fatalf("exit=%d stderr=%s", code, stderr())
	}
	got := assertInvocationWaitRecovery(t, stderr(), "[REDACTED]")
	if strings.Contains(stderr(), secret) || !strings.Contains(got.Hint, "Use a read-scoped credential [REDACTED]") || got.Code != "scope_required" || got.Status != 403 {
		t.Fatalf("lost server remediation or leaked credential: %s", stderr())
	}
}
