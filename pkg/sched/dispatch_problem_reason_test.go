// spec: §4.9

package sched

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestDispatchFailureCarriesProblemReason pins H9-153: a resumed workflow
// step pinned to an older revision failed three times with only "sched:
// invocation: gateway returned 502" while gatewayd-internal knew the reason
// (plan_limit_concurrency: the app's max_concurrency was used by two other
// revisions). A problem body must reach the error; a plain-text body must not.
func TestDispatchFailureCarriesProblemReason(t *testing.T) {
	cases := []struct {
		name, contentType, body, want, notWant string
	}{
		{name: "problem", contentType: "application/problem+json",
			body: `{"code":"plan_limit_concurrency","detail":"scale app max_concurrency is 2; 2 already live.","status":429}`,
			want: "gateway returned 502 (plan_limit_concurrency: scale app max_concurrency is 2; 2 already live.)"},
		{name: "plain text stays internal", contentType: "text/plain; charset=utf-8",
			body: "dial unix /run/faas/vmmd.sock: connection refused", want: "gateway returned 502", notWant: "/run/faas"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			h := &httpGatewaySynth{client: srv.Client(), basePrefix: srv.URL}
			_, err := h.InvokeWithWake(t.Context(), "app-1",
				state.Invocation{ID: "inv-1", AppID: "app-1", Source: state.InvocationAsyncInvoke, Method: http.MethodPost, Path: "/"},
				WakeResult{InstanceID: "instance-1", NodeID: "node-1"})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
			if tc.notWant != "" && strings.Contains(err.Error(), tc.notWant) {
				t.Fatalf("err %q leaks %q", err, tc.notWant)
			}
			if errors.Is(err, ErrPermanentInvoke) {
				t.Fatalf("a 502 dispatch failure must stay retryable: %v", err)
			}
		})
	}
}
