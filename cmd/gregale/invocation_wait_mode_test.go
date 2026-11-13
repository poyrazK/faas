package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestInvocationWaitModeSelection(t *testing.T) {
	for _, tc := range []struct {
		name, endpoint, resultField string
		args                        []string
		poll, work                  bool
	}{
		{"implicit async", "/invoke/async", "state", []string{"invocation", "run", "api", "--wait"}, true, false},
		{"wait with retry key", "/invoke/async", "state", []string{"invocation", "run", "api", "--wait", "--idempotency-key", "invoice-4821"}, true, false},
		{"explicit async", "/invoke/async", "state", []string{"invocation", "run", "api", "--async", "--wait"}, true, false},
		{"earlier name", "/invoke/async", "state", []string{"invoke", "api", "--wait"}, true, false},
		{"flags first", "/invoke/async", "state", []string{"invocation", "run", "--wait", "--timeout=0", "--interval=1ms", "api"}, true, false},
		{"queue only", "/invoke/async", "status_url", []string{"invocation", "run", "api", "--async"}, false, false},
		{"wait disabled", "/invoke/async", "status_url", []string{"invocation", "run", "api", "--async", "--wait=false"}, false, false},
		{"sync explicit", "/invoke", "status", []string{"invocation", "run", "api", "--async=false", "--wait=false"}, false, false},
		{"work policy", "/invoke/async", "state", []string{"invocation", "run", "api", "--wait", "--work-policy", "index", "--work-key", `"doc-1"`}, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			main.ResetJSONOut(t)
			var posts, gets atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					posts.Add(1)
					if tc.name == "wait with retry key" && r.Header.Get("Idempotency-Key") != "invoice-4821" {
						t.Errorf("implicit async lost explicit retry key")
					}
					if r.URL.Path != "/v1/apps/api"+tc.endpoint {
						t.Errorf("submission path=%s", r.URL.Path)
					}
					var req api.InvokeRequest
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Errorf("decode submission: %v", err)
					}
					if tc.work && (req.Work == nil || req.Work.Policy != "index" || string(req.Work.Key) != `"doc-1"`) {
						t.Errorf("implicit async lost work policy: %+v", req.Work)
					}
					if strings.HasSuffix(r.URL.Path, "/async") {
						_ = json.NewEncoder(w).Encode(api.AsyncInvokeResponse{ID: "inv-mode", StatusURL: "/v1/invocations/inv-mode"})
					} else {
						_ = json.NewEncoder(w).Encode(api.InvokeResponse{ID: "inv-mode", Status: "completed"})
					}
					return
				}
				gets.Add(1)
				if r.Header.Get("Idempotency-Key") == "invoice-4821" {
					t.Error("submission retry key leaked into polling")
				}
				if r.Method != http.MethodGet || r.URL.Path != "/v1/invocations/inv-mode" {
					t.Errorf("unexpected polling request: %s %s", r.Method, r.URL.Path)
				}
				_ = json.NewEncoder(w).Encode(api.Invocation{ID: "inv-mode", State: "completed"})
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			t.Setenv("FAAS_TOKEN", "wait-mode-test-token")
			out, stderr, restore := main.SwapIO(t)
			defer restore()
			code := main.Run(append([]string{"--json"}, tc.args...))
			wantGets := int32(0)
			if tc.poll {
				wantGets = 1
			}
			var record map[string]json.RawMessage
			if code != 0 || posts.Load() != 1 || gets.Load() != wantGets || json.Unmarshal(out.Bytes(), &record) != nil || len(record[tc.resultField]) == 0 {
				t.Fatalf("exit=%d posts=%d gets=%d stdout=%s stderr=%s", code, posts.Load(), gets.Load(), out.String(), stderr())
			}
		})
	}
}

func TestInvocationWaitDurationValidationIsShared(t *testing.T) {
	main.ResetJSONOut(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "wait-mode-test-token")
	for _, prefix := range [][]string{{"invocation", "run", "api", "--wait"}, {"invocation", "wait", "inv-mode"}} {
		for _, duration := range [][]string{{"--timeout=-1s"}, {"--interval=0"}, {"--interval=-1s"}} {
			t.Run(strings.Join(append(append([]string{}, prefix...), duration...), " "), func(t *testing.T) {
				out, stderr, restore := main.SwapIO(t)
				defer restore()
				args := append(append([]string{"--json"}, prefix...), duration...)
				if code := main.Run(args); code != 1 || calls.Load() != 0 || out.Len() != 0 {
					t.Fatalf("exit=%d requests=%d stdout=%s stderr=%s", code, calls.Load(), out.String(), stderr())
				}
			})
		}
	}
}
