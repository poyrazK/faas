package main_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

const errorCorrelationID = "019a767e7f0070008000000000000001"

func TestErrorRequestIDOnInvocationWaitRecovery(t *testing.T) {
	for _, command := range []struct {
		name  string
		args  []string
		posts int32
	}{
		{"standalone", []string{"invocation", "wait", "inv-correlation"}, 0},
		{"submitted", []string{"invocation", "run", "api", "--wait"}, 1},
	} {
		for _, header := range []struct{ name, value, want string }{
			{"valid", errorCorrelationID, errorCorrelationID},
			{"invalid", "invalid-correlation", ""},
			{"missing", "", ""},
		} {
			t.Run(command.name+"/"+header.name, func(t *testing.T) {
				main.SetupConnectionProfiles(t)
				t.Setenv("FAAS_TOKEN", main.TestAPIKey('a'))
				var posts, gets atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodPost && r.URL.Path == "/v1/apps/api/invoke/async" {
						posts.Add(1)
						_ = json.NewEncoder(w).Encode(api.AsyncInvokeResponse{ID: "inv-correlation"})
						return
					}
					if r.Method != http.MethodGet || r.URL.Path != "/v1/invocations/inv-correlation" {
						t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					}
					gets.Add(1)
					if header.value != "" {
						w.Header().Set(api.RequestIDHeader, header.value)
					}
					w.Header().Set("Content-Type", "application/problem+json")
					w.WriteHeader(http.StatusServiceUnavailable)
					_ = json.NewEncoder(w).Encode(api.Problem{Status: 503, Code: "service_unavailable", Title: "Try later"})
				}))
				defer server.Close()
				t.Setenv("FAAS_API", server.URL)
				out, stderr, restore := main.SwapIO(t)
				defer restore()
				code := main.Run(append([]string{"--json"}, command.args...))
				var got struct {
					Code      string `json:"code"`
					RequestID string `json:"request_id"`
					ExitCode  int    `json:"exit_code"`
					Recovery  struct {
						InvocationID string `json:"invocation_id"`
					} `json:"recovery"`
				}
				if code != 3 || posts.Load() != command.posts || gets.Load() != 1 || out.Len() != 0 || json.Unmarshal([]byte(stderr()), &got) != nil || got.Code != "service_unavailable" || got.ExitCode != 3 || got.RequestID != header.want || got.Recovery.InvocationID != "inv-correlation" {
					t.Fatalf("wait lost correlation or recovery: exit=%d posts=%d gets=%d stdout=%s stderr=%s", code, posts.Load(), gets.Load(), out.String(), stderr())
				}
				if header.want == "" && strings.Contains(stderr(), `"request_id"`) {
					t.Fatalf("untrusted or missing request ID must be omitted: %s", stderr())
				}
			})
		}
	}
}

func TestErrorRequestIDAfterReadRetries(t *testing.T) {
	for _, finalID := range []string{"019a767e7f0070008000000000000002", ""} {
		t.Run("final="+finalID, func(t *testing.T) {
			main.SetupConnectionProfiles(t)
			t.Setenv("FAAS_TOKEN", main.TestAPIKey('a'))
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				id := finalID
				if requests.Add(1) == 1 {
					id = errorCorrelationID
				}
				if id != "" {
					w.Header().Set(api.RequestIDHeader, id)
				}
				w.Header().Set("Retry-After", "0")
				w.Header().Set("Content-Type", "application/problem+json")
				w.WriteHeader(http.StatusServiceUnavailable)
				_ = json.NewEncoder(w).Encode(api.Problem{Status: 503, Code: "service_unavailable", Title: "Try later"})
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			code, stdout, stderr := runHTTPDebugCLI(t, "--http-retries", "1", "--json", "billing", "usage", "summary")
			var got struct {
				Code      string `json:"code"`
				RequestID string `json:"request_id"`
				ExitCode  int    `json:"exit_code"`
			}
			if code != 3 || requests.Load() != 2 || stdout != "" || json.Unmarshal([]byte(stderr), &got) != nil || got.RequestID != finalID || got.Code != "service_unavailable" || got.ExitCode != 3 {
				t.Fatalf("wrong final-attempt diagnostic: exit=%d requests=%d stdout=%q stderr=%q", code, requests.Load(), stdout, stderr)
			}
			if finalID == "" && strings.Contains(stderr, `"request_id"`) {
				t.Fatalf("reused an earlier attempt's request ID: %s", stderr)
			}
		})
	}
}

func TestErrorRequestIDInTextAndJSON(t *testing.T) {
	main.SetupConnectionProfiles(t)
	t.Setenv("FAAS_TOKEN", main.TestAPIKey('a'))
	for _, tc := range []struct {
		name, body string
		ids        []string
		want       string
	}{
		{"json", `{"status":503,"code":"service_unavailable","detail":"try later","request_id":"body-private"}`, []string{errorCorrelationID}, errorCorrelationID},
		{"proxy", `<html>proxy-private</html>`, []string{errorCorrelationID}, errorCorrelationID},
		{"missing", `{"status":503,"code":"service_unavailable","request_id":"019a767e7f0070008000000000000001"}`, nil, ""},
		{"invalid", `{}`, []string{"header-private"}, ""},
		{"duplicate", `{}`, []string{errorCorrelationID, errorCorrelationID}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/usage/summary" {
					t.Errorf("unexpected API path %s", r.URL.Path)
				}
				for _, id := range tc.ids {
					w.Header().Add(api.RequestIDHeader, id)
				}
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			for _, jsonMode := range []bool{false, true} {
				args := []string{"billing", "usage", "summary"}
				if jsonMode {
					args = append([]string{"--json"}, args...)
				}
				code, stdout, stderr := runHTTPDebugCLI(t, args...)
				if code != 3 || stdout != "" {
					t.Fatalf("exit=%d stdout=%q stderr=%s", code, stdout, stderr)
				}
				if jsonMode {
					var problem struct {
						ID       string `json:"request_id"`
						Status   int    `json:"status"`
						Category string `json:"category"`
						Exit     int    `json:"exit_code"`
					}
					if err := json.Unmarshal([]byte(stderr), &problem); err != nil {
						t.Fatal(err)
					}
					if problem.ID != tc.want || problem.Status != 503 || problem.Exit != 3 || problem.Category != "temporary_failure" {
						t.Fatalf("incorrect error envelope: %s", stderr)
					}
					if tc.want == "" && strings.Contains(stderr, `"request_id"`) {
						t.Fatal("missing ID should be omitted")
					}
				} else if tc.want != "" {
					if strings.Count(stderr, "Request ID: "+tc.want) != 1 {
						t.Fatalf("request ID missing or repeated: %s", stderr)
					}
				} else if strings.Contains(stderr, "Request ID:") {
					t.Fatalf("untrusted ID rendered: %s", stderr)
				}
				for _, private := range []string{"header-private", "proxy-private", "body-private"} {
					if strings.Contains(stderr, private) {
						t.Fatal("untrusted request ID or proxy text leaked")
					}
				}
			}
		})
	}
}

func TestErrorRequestIDRedactionAndJSONWriters(t *testing.T) {
	main.SetupConnectionProfiles(t)
	t.Setenv("FAAS_TOKEN", main.TestAPIKey('a'))
	p := api.Problem{Status: 503, Code: "service_unavailable", Title: "Unavailable", Detail: "token=" + main.TestAPIKey('a')}
	p = *p.WithRequestID(errorCorrelationID)
	safe := main.RedactProblem(p)
	if safe.Detail == p.Detail || !strings.Contains(safe.Detail, "[REDACTED]") || safe.RequestID() != errorCorrelationID {
		t.Fatal("redaction lost correlation or retained credentials")
	}
	if p.Detail != "token="+main.TestAPIKey('a') || p.RequestID() != errorCorrelationID {
		t.Fatal("redaction modified the SDK problem")
	}
	for _, writer := range []func(io.Writer, api.Problem) error{main.WriteJSONProblemTo, func(w io.Writer, p api.Problem) error { return main.WriteJSONProblemWithExit(w, p, 3) }} {
		var out bytes.Buffer
		if err := writer(&out, safe); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), `"request_id":"`+errorCorrelationID+`"`) || strings.Contains(out.String(), main.TestAPIKey('a')) {
			t.Fatalf("correlation or redaction lost: %s", out.String())
		}
	}
	// A shaped ID can still equal a known credential; suppress it entirely.
	t.Setenv("FAAS_TOKEN", errorCorrelationID)
	p.Detail = "unavailable"
	safe = main.RedactProblem(p)
	if safe.RequestID() != "" {
		t.Fatal("credential-shaped correlation ID retained")
	}
	for _, writer := range []func(io.Writer, api.Problem) error{main.WriteJSONProblemTo, func(w io.Writer, p api.Problem) error { return main.WriteJSONProblemWithExit(w, p, 3) }} {
		var out bytes.Buffer
		if err := writer(&out, p); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), errorCorrelationID) || strings.Contains(out.String(), `"request_id"`) {
			t.Fatal("JSON writer leaked a credential as an ID")
		}
	}
}

func TestErrorRequestIDSuccessAndLocalFailures(t *testing.T) {
	main.SetupConnectionProfiles(t)
	t.Setenv("FAAS_TOKEN", main.TestAPIKey('a'))
	var withID atomic.Bool
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		if withID.Load() {
			w.Header().Set(api.RequestIDHeader, errorCorrelationID)
		}
		_, _ = io.WriteString(w, `{}`)
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	code, baseline, stderr := runHTTPDebugCLI(t, "--json", "billing", "usage", "summary")
	if code != 0 || stderr != "" {
		t.Fatalf("baseline exit=%d stderr=%s", code, stderr)
	}
	withID.Store(true)
	if code, stdout, stderr := runHTTPDebugCLI(t, "--json", "billing", "usage", "summary"); code != 0 || stdout != baseline || stderr != "" {
		t.Fatalf("success output changed: exit=%d stderr=%s", code, stderr)
	}
	before := calls.Load()
	if code, _, stderr := runHTTPDebugCLI(t, "--json", "billing", "usage", "summary", "--unknown"); code == 0 || strings.Contains(stderr, `"request_id"`) || calls.Load() != before {
		t.Fatalf("local failure invented an ID: exit=%d stderr=%s", code, stderr)
	}
	t.Setenv("FAAS_API", "http://127.0.0.1:1")
	if code, _, stderr := runHTTPDebugCLI(t, "--json", "billing", "usage", "summary"); code != 3 || strings.Contains(stderr, `"request_id"`) {
		t.Fatalf("transport failure invented an ID: exit=%d stderr=%s", code, stderr)
	}
}
