// Package main — builder-VM network preflight and build exit classification.
//
// This file is build-tag-free so the pure helpers are unit-testable on any
// host; main_linux.go calls them from the builder path.

package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// builderHTTPSPreflightBudget bounds one HTTPS reachability check from a
// builder VM. A fresh Firecracker guest's first routed TLS connection can take
// several seconds when the node's network path has just come up. The Node
// toolchain check was given 15 s for that reason, but its http.Client carried
// a fixed 5 s Timeout that cut the budget; after every production rollout the
// first Node build on each node failed in ~10 s as "timeout: build exited 124".
var builderHTTPSPreflightBudget = 15 * time.Second

// builderHTTPSPreflightAttempts and builderHTTPSPreflightRetryDelay retry a
// preflight whose request failed in transport. The first Node build after a
// compute rollout still failed on 6 of 8 production rollouts with a 15 s
// connect timeout to nodejs.org, while a build started two seconds later on
// the same node reached the network (production-us hunt #5, H5-53). A fresh
// attempt after the first one times out covers that window; an endpoint that
// answers with an unhealthy status is not retried.
const builderHTTPSPreflightAttempts = 3

var builderHTTPSPreflightRetryDelay = 2 * time.Second

// builderNetworkError marks a failed builder network preflight. It is an
// infrastructure failure of the builder's network path, not a build that ran
// out of time, so it must not be recorded as exit 124 / FailureTimeout.
type builderNetworkError struct{ err error }

func (e builderNetworkError) Error() string { return e.err.Error() }
func (e builderNetworkError) Unwrap() error { return e.err }

// builderHTTPSPreflight GETs url within builderHTTPSPreflightBudget per
// attempt and reports an error unless healthy accepts the response status.
// Each request is bounded by its context only; there is no shorter client
// timeout. A transport failure is retried up to builderHTTPSPreflightAttempts.
//
// The error names each failed attempt's phase (DNS lookup, TCP connect, TLS
// handshake, waiting for response), the address it dialled and how long it
// took (#6624): two production Node builds failed on three straight
// nodejs.org timeouts reported only as "context deadline exceeded", which
// could not tell a DNS, egress or upstream problem apart.
func builderHTTPSPreflight(parent context.Context, name, url string, healthy func(status int) bool) error {
	var err error
	var attempts []string
	for attempt := 1; attempt <= builderHTTPSPreflightAttempts; attempt++ {
		var transport bool
		var detail string
		transport, detail, err = builderHTTPSPreflightOnce(parent, url, healthy)
		if err != nil {
			attempts = append(attempts, fmt.Sprintf("attempt %d: %s", attempt, detail))
		}
		if err == nil || !transport || parent.Err() != nil {
			break
		}
		if attempt < builderHTTPSPreflightAttempts {
			select {
			case <-time.After(builderHTTPSPreflightRetryDelay):
			case <-parent.Done():
			}
		}
	}
	if err != nil {
		return builderNetworkError{fmt.Errorf("%s HTTPS preflight failed (%s): %w", name, strings.Join(attempts, "; "), err)}
	}
	return nil
}

// preflightStage is how far one preflight request got.
type preflightStage int

const (
	stageStart preflightStage = iota
	stageResolved
	stageConnected
	stageSecured
	stageRequestSent
	stageResponding
)

// failing names the phase a request that reached s was in when it failed.
func (s preflightStage) failing() string {
	switch s {
	case stageStart:
		return "DNS lookup"
	case stageResolved:
		return "TCP connect"
	case stageConnected:
		return "TLS handshake"
	case stageSecured:
		return "sending request"
	case stageRequestSent:
		return "waiting for response"
	default:
		return "reading response"
	}
}

// preflightTrace records the furthest stage a request reached and the address
// it dialled. httptrace hooks can run on other goroutines.
type preflightTrace struct {
	mu      sync.Mutex
	reached preflightStage
	addr    string
}

func (t *preflightTrace) reach(s preflightStage) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if s > t.reached {
		t.reached = s
	}
}

func (t *preflightTrace) clientTrace() *httptrace.ClientTrace {
	return &httptrace.ClientTrace{
		DNSDone: func(info httptrace.DNSDoneInfo) {
			if info.Err == nil {
				t.reach(stageResolved)
			}
		},
		ConnectStart: func(_, addr string) {
			t.mu.Lock()
			t.addr = addr
			t.mu.Unlock()
			t.reach(stageResolved) // an IP literal skips DNS
		},
		ConnectDone: func(_, _ string, err error) {
			if err == nil {
				t.reach(stageConnected)
			}
		},
		TLSHandshakeDone: func(_ tls.ConnectionState, err error) {
			if err == nil {
				t.reach(stageSecured)
			}
		},
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			if info.Err == nil {
				t.reach(stageRequestSent)
			}
		},
		GotFirstResponseByte: func() { t.reach(stageResponding) },
	}
}

// describe summarises a failed attempt: phase, dialled address and duration.
func (t *preflightTrace) describe(elapsed time.Duration) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	d := t.reached.failing()
	if t.addr != "" {
		d += " to " + t.addr
	}
	return d + " failed after " + elapsed.Round(10*time.Millisecond).String()
}

// builderHTTPSPreflightOnce runs one preflight request. transport reports a
// failure before any response arrived; detail describes a failure.
func builderHTTPSPreflightOnce(parent context.Context, url string, healthy func(status int) bool) (transport bool, detail string, err error) {
	ctx, cancel := context.WithTimeout(parent, builderHTTPSPreflightBudget)
	defer cancel()
	trace := &preflightTrace{}
	ctx = httptrace.WithClientTrace(ctx, trace.clientTrace())
	start := time.Now()
	err = func() error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			transport = true
			return err
		}
		defer func() { _ = resp.Body.Close() }()
		_, _ = io.Copy(io.Discard, resp.Body)
		if !healthy(resp.StatusCode) {
			return fmt.Errorf("status %s", resp.Status)
		}
		return nil
	}()
	switch {
	case err == nil:
		return false, "", nil
	case !transport:
		return false, err.Error(), err
	default:
		return true, trace.describe(time.Since(start)), err
	}
}

// buildExitStatus maps a build attempt's error to the exit code and failure
// class guest-init records in build-done.json.
func buildExitStatus(runErr error) (int, string) {
	if runErr == nil {
		return 0, ""
	}
	var network builderNetworkError
	if errors.As(runErr, &network) {
		return 1, "FailureInfra"
	}
	exitCode := 1
	var ee *exec.ExitError
	if errors.As(runErr, &ee) {
		exitCode = ee.ExitCode()
	} else if errors.Is(runErr, context.DeadlineExceeded) {
		exitCode = 124
	}
	return exitCode, classify(exitCode)
}

// classify maps a build exit code to the failure class builderd records.
func classify(exitCode int) string {
	switch exitCode {
	case 137:
		return "FailureOOM"
	case 124:
		return "FailureTimeout"
	case 0:
		return ""
	default:
		return "FailureUserError"
	}
}
