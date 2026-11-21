package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Production: after every rollout the first Node build on each node failed
// in ~10 s with "timeout: build exited 124". The Node preflight had a 15 s
// context, but its http.Client Timeout of 5 s cut it, so a first TLS
// connection slower than 5 s failed the build. The check must use its whole
// budget.
func TestBuilderHTTPSPreflightUsesWholeBudget(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(5500 * time.Millisecond):
		case <-r.Context().Done():
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	ok := func(status int) bool { return status < http.StatusBadRequest }
	if err := builderHTTPSPreflight(context.Background(), "slow", srv.URL, ok); err != nil {
		t.Fatalf("a 5.5 s first response must fit the %s budget: %v", builderHTTPSPreflightBudget, err)
	}
}

func TestBuilderHTTPSPreflightReportsUnhealthyStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	err := builderHTTPSPreflight(context.Background(), "GHCR", srv.URL,
		func(status int) bool { return status < http.StatusInternalServerError })
	var network builderNetworkError
	if !errors.As(err, &network) {
		t.Fatalf("err = %v, want a builderNetworkError", err)
	}
}

func TestBuildExitStatus(t *testing.T) {
	timedOutPreflight := builderNetworkError{fmt.Errorf("node toolchain HTTPS preflight: %w", context.DeadlineExceeded)}
	for _, tc := range []struct {
		name      string
		err       error
		wantCode  int
		wantClass string
	}{
		{"success", nil, 0, ""},
		// A network preflight that timed out is an infrastructure failure of
		// the builder, not a build that exhausted its time budget.
		{"preflight timeout", timedOutPreflight, 1, "FailureInfra"},
		{"build deadline", fmt.Errorf("build: %w", context.DeadlineExceeded), 124, "FailureTimeout"},
		{"other", errors.New("start build command: boom"), 1, "FailureUserError"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, class := buildExitStatus(tc.err)
			if code != tc.wantCode || class != tc.wantClass {
				t.Fatalf("buildExitStatus = (%d, %q), want (%d, %q)", code, class, tc.wantCode, tc.wantClass)
			}
		})
	}
}

// production-us hunt #5 (H5-53): the first Node build after a compute
// rollout timed out reaching nodejs.org while a build two seconds later on
// the same node got through. A transport failure is retried with a fresh
// request; an unhealthy status is not.
func TestBuilderHTTPSPreflightRetriesTransportFailures(t *testing.T) {
	saved := builderHTTPSPreflightRetryDelay
	builderHTTPSPreflightRetryDelay = 10 * time.Millisecond
	t.Cleanup(func() { builderHTTPSPreflightRetryDelay = saved })

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close() // the first request dies before a response
			}
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	ok := func(status int) bool { return status < http.StatusBadRequest }
	if err := builderHTTPSPreflight(context.Background(), "node toolchain", srv.URL, ok); err != nil {
		t.Fatalf("preflight = %v, want the retry to succeed", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("requests = %d, want 2", got)
	}

	calls.Store(0)
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer bad.Close()
	if err := builderHTTPSPreflight(context.Background(), "GHCR", bad.URL, func(status int) bool { return status < http.StatusInternalServerError }); err == nil {
		t.Fatal("an unhealthy status passed the preflight")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("requests to an unhealthy endpoint = %d, want 1 (no retry)", got)
	}
}

// #6624: two production Node builds failed on three nodejs.org preflight
// timeouts reported only as "context deadline exceeded". The error must name
// every attempt's failed phase, the dialled address and the time it took, so
// a DNS, egress and upstream stall can be told apart from build logs alone.
func TestBuilderHTTPSPreflightNamesFailedPhasePerAttempt(t *testing.T) {
	savedBudget, savedDelay := builderHTTPSPreflightBudget, builderHTTPSPreflightRetryDelay
	builderHTTPSPreflightBudget, builderHTTPSPreflightRetryDelay = 200*time.Millisecond, 10*time.Millisecond
	t.Cleanup(func() { builderHTTPSPreflightBudget, builderHTTPSPreflightRetryDelay = savedBudget, savedDelay })
	ok := func(status int) bool { return status < http.StatusBadRequest }

	// Connected, request sent, no answer: an upstream stall.
	stall := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer stall.Close()
	err := builderHTTPSPreflight(context.Background(), "node toolchain", stall.URL, ok)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want it to wrap context.DeadlineExceeded", err)
	}
	msg := err.Error()
	addr := strings.TrimPrefix(stall.URL, "http://")
	for i := 1; i <= builderHTTPSPreflightAttempts; i++ {
		want := fmt.Sprintf("attempt %d: waiting for response to %s failed after ", i, addr)
		if !strings.Contains(msg, want) {
			t.Errorf("error lacks %q:\n%s", want, msg)
		}
	}
	if !strings.HasPrefix(msg, "node toolchain HTTPS preflight failed (") {
		t.Errorf("error lost its preflight prefix:\n%s", msg)
	}

	// Nothing listening: the egress path or the endpoint refused the connect.
	closed := httptest.NewServer(http.NotFoundHandler())
	refusedAddr := strings.TrimPrefix(closed.URL, "http://")
	closed.Close()
	err = builderHTTPSPreflight(context.Background(), "GHCR", closed.URL, ok)
	if want := "attempt 1: TCP connect to " + refusedAddr + " failed after "; err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v, want it to contain %q", err, want)
	}

	// An unhealthy status keeps its status text and is not retried.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
	defer bad.Close()
	err = builderHTTPSPreflight(context.Background(), "GHCR", bad.URL, func(status int) bool { return status < http.StatusInternalServerError })
	if err == nil || !strings.Contains(err.Error(), "(attempt 1: status 502 Bad Gateway)") {
		t.Fatalf("err = %v, want the single attempt's 502 status", err)
	}
}
