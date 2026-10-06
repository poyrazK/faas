package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
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
