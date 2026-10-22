// spec: §4.9

package sched

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestInvocationAttemptTimeoutIsReadable pins H9-135: an async invocation
// that outlived the dispatch limit recorded last_error as `sched: invocation
// do: Post "http://unix/v1/invocations:dispatch": context deadline exceeded
// (Client.Timeout …)`, naming an internal URL and not the limit. It must be a
// dispatch timeout (durable outcome "timeout") that states the limit.
func TestInvocationAttemptTimeoutIsReadable(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)
	client := srv.Client()
	client.Timeout = 50 * time.Millisecond
	h := &httpGatewaySynth{client: client, basePrefix: srv.URL}

	_, err := h.InvokeWithWake(t.Context(), "app-1",
		state.Invocation{ID: "inv-1", AppID: "app-1", Source: state.InvocationAsyncInvoke, Method: http.MethodPost, Path: "/"},
		WakeResult{InstanceID: "instance-1", NodeID: "node-1"})
	if !errors.Is(err, ErrDispatchTimeout) {
		t.Fatalf("err = %v, want ErrDispatchTimeout", err)
	}
	if errors.Is(err, ErrPermanentInvoke) {
		t.Fatalf("a timed-out attempt must stay retryable: %v", err)
	}
	msg := err.Error()
	for _, leak := range []string{srv.URL, "/v1/invocations:dispatch", "Client.Timeout"} {
		if strings.Contains(msg, leak) {
			t.Fatalf("last_error %q leaks %q", msg, leak)
		}
	}
	if !strings.Contains(msg, "30s dispatch limit") {
		t.Fatalf("last_error %q does not state the limit", msg)
	}

	// A caller that gave up keeps its own cause.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = h.InvokeWithWake(ctx, "app-1",
		state.Invocation{ID: "inv-2", AppID: "app-1", Source: state.InvocationAsyncInvoke, Method: http.MethodPost, Path: "/"},
		WakeResult{InstanceID: "instance-1", NodeID: "node-1"})
	if errors.Is(err, ErrDispatchTimeout) || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled caller err = %v, want context.Canceled", err)
	}
}
