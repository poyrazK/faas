// Tests for the retry decorator (pkg/mail/retry.go). The decorator's
// retry budget is in pkg/api/limits.go (MailRetryMaxAttempts,
// MailRetryBaseDelayMS, MailRetryMaxWallClockMS); this file pins the
// behaviour the budget encodes.
//
// Strategy: inject a fake Sender + a fake clock + a recording sleep
// so the suite never actually waits MailRetryMaxWallClockMS per row.
// The clock advances by the sleep duration so the wall-clock-cap row
// can fast-forward past the budget without any real elapsed time.
package mail_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mail"
)

// fakeSender returns a programmed sequence of errors then nil, and
// records every call. The first nil terminates the sequence (so a
// test can write "transient, transient, ok" without specifying the
// length up-front).
type fakeSender struct {
	mu      sync.Mutex
	results []error
	calls   int
}

func (f *fakeSender) Send(_ context.Context, _ mail.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	idx := f.calls
	f.calls++
	if idx >= len(f.results) {
		return nil
	}
	return f.results[idx]
}

func (f *fakeSender) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// fakeClock returns the head of `times` per call, defaulting to
// start when the slice is exhausted. sleep() advances the head.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	sleeps []time.Duration
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Sleep(_ context.Context, d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sleeps = append(c.sleeps, d)
	c.now = c.now.Add(d)
}

func (c *fakeClock) Sleeps() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]time.Duration, len(c.sleeps))
	copy(out, c.sleeps)
	return out
}

// TestRetryingSender_SuccessOnFirstAttempt confirms a healthy inner
// sender is called exactly once — no retry, no sleep, no metric.
func TestRetryingSender_SuccessOnFirstAttempt(t *testing.T) {
	t.Parallel()
	inner := &fakeSender{results: []error{nil}}
	clk := &fakeClock{now: time.Unix(0, 0)}
	r := &mail.RetryingSender{
		Inner:         inner,
		TransportName: mail.TransportResend,
		Log:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:           clk.Now,
		Sleep:         clk.Sleep,
	}
	if err := r.Send(context.Background(), mail.Message{}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := inner.Calls(); got != 1 {
		t.Errorf("inner.Calls = %d, want 1", got)
	}
	if got := clk.Sleeps(); len(got) != 0 {
		t.Errorf("sleeps = %v, want none", got)
	}
}

// TestRetryingSender_RecoversOnSecondAttempt is the happy retry:
// first attempt transient, second attempt succeeds.
func TestRetryingSender_RecoversOnSecondAttempt(t *testing.T) {
	t.Parallel()
	te := &mail.TransientError{Status: 503}
	inner := &fakeSender{results: []error{te, nil}}
	clk := &fakeClock{now: time.Unix(0, 0)}
	r := &mail.RetryingSender{
		Inner:         inner,
		TransportName: mail.TransportResend,
		Log:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:           clk.Now,
		Sleep:         clk.Sleep,
	}
	if err := r.Send(context.Background(), mail.Message{}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := inner.Calls(); got != 2 {
		t.Errorf("inner.Calls = %d, want 2", got)
	}
	if got := len(clk.Sleeps()); got != 1 {
		t.Errorf("sleeps = %d, want 1", got)
	}
}

// TestRetryingSender_ExhaustsAttempts pins the hard cap on retry
// count: after MailRetryMaxAttempts attempts the last transient
// error is returned and no further sleep is recorded.
func TestRetryingSender_ExhaustsAttempts(t *testing.T) {
	t.Parallel()
	te := &mail.TransientError{Status: 503}
	// Build a result slice of length MailRetryMaxAttempts so every
	// attempt returns a transient error.
	results := make([]error, api.MailRetryMaxAttempts)
	for i := range results {
		results[i] = te
	}
	inner := &fakeSender{results: results}
	clk := &fakeClock{now: time.Unix(0, 0)}
	r := &mail.RetryingSender{
		Inner:         inner,
		TransportName: mail.TransportResend,
		Log:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:           clk.Now,
		Sleep:         clk.Sleep,
	}
	err := r.Send(context.Background(), mail.Message{})
	if !errors.Is(err, mail.ErrTransient) {
		t.Errorf("err = %v, want errors.Is(err, mail.ErrTransient)", err)
	}
	if got, want := inner.Calls(), api.MailRetryMaxAttempts; got != want {
		t.Errorf("inner.Calls = %d, want %d", got, want)
	}
	// One sleep per (failed attempt → next attempt); with N
	// failed attempts we record N-1 sleeps.
	if got, want := len(clk.Sleeps()), api.MailRetryMaxAttempts-1; got != want {
		t.Errorf("sleeps = %d, want %d", got, want)
	}
}

// TestRetryingSender_NonTransientErrorReturnsImmediately pins the
// "respect the upstream" contract: a 4xx (non-transient) is
// returned to the caller without a second attempt.
func TestRetryingSender_NonTransientErrorReturnsImmediately(t *testing.T) {
	t.Parallel()
	inner := &fakeSender{results: []error{
		errors.New("mail: resend: 422 validation_error: bad address"),
		nil, // sentinel — must not be reached
	}}
	clk := &fakeClock{now: time.Unix(0, 0)}
	r := &mail.RetryingSender{
		Inner:         inner,
		TransportName: mail.TransportResend,
		Log:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:           clk.Now,
		Sleep:         clk.Sleep,
	}
	err := r.Send(context.Background(), mail.Message{})
	if err == nil {
		t.Fatal("expected error on 422")
	}
	if got := inner.Calls(); got != 1 {
		t.Errorf("inner.Calls = %d, want 1 (no retry on permanent)", got)
	}
	if got := clk.Sleeps(); len(got) != 0 {
		t.Errorf("sleeps = %v, want none (no retry on permanent)", got)
	}
}

// TestRetryingSender_HonoursRetryAfter pins the provider-honours
// path: when *TransientError.RetryAfter is shorter than the
// computed backoff, the decorator sleeps for RetryAfter (not the
// backoff).
func TestRetryingSender_HonoursRetryAfter(t *testing.T) {
	t.Parallel()
	te := &mail.TransientError{Status: 429, RetryAfter: 10 * time.Millisecond}
	inner := &fakeSender{results: []error{te, nil}}
	clk := &fakeClock{now: time.Unix(0, 0)}
	r := &mail.RetryingSender{
		Inner:         inner,
		TransportName: mail.TransportResend,
		Log:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:           clk.Now,
		Sleep:         clk.Sleep,
		Backoff: func(int, time.Duration) time.Duration {
			return 20 * time.Millisecond
		},
	}
	if err := r.Send(context.Background(), mail.Message{}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	sleeps := clk.Sleeps()
	if len(sleeps) != 1 {
		t.Fatalf("sleeps = %v, want 1 entry", sleeps)
	}
	// 10ms Retry-After must beat the (random) backoff and become
	// the recorded delay.
	if sleeps[0] != 10*time.Millisecond {
		t.Errorf("sleep[0] = %s, want 10ms (Retry-After honoured)", sleeps[0])
	}
}

// TestRetryingSender_RetryAfterClampedByBudget pins the budget cap:
// a Retry-After longer than the remaining wall-clock budget is
// truncated so the handler never blocks past MailRetryMaxWallClock.
func TestRetryingSender_RetryAfterClampedByBudget(t *testing.T) {
	t.Parallel()
	te := &mail.TransientError{
		Status:     429,
		RetryAfter: 60 * time.Second, // huge
	}
	// Every Send call returns te so the decorator hits the budget
	// wall instead of recovering on the next attempt.
	results := make([]error, api.MailRetryMaxAttempts+1)
	for i := range results {
		results[i] = te
	}
	inner := &fakeSender{results: results}
	clk := &fakeClock{now: time.Unix(0, 0)}
	r := &mail.RetryingSender{
		Inner:         inner,
		TransportName: mail.TransportResend,
		Log:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:           clk.Now,
		Sleep:         clk.Sleep,
	}
	if err := r.Send(context.Background(), mail.Message{}); err == nil {
		t.Fatal("expected error after budget exhaustion")
	}
	// Decorator sleeps at most (MaxAttempts-1) times; every
	// recorded delay must not exceed the wall-clock budget.
	sleeps := clk.Sleeps()
	if len(sleeps) > api.MailRetryMaxAttempts-1 {
		t.Errorf("sleeps = %v, want ≤ %d entries", sleeps, api.MailRetryMaxAttempts-1)
	}
	for i, d := range sleeps {
		wallClock := time.Duration(api.MailRetryMaxWallClockMS) * time.Millisecond
		if d > wallClock {
			t.Errorf("sleeps[%d] = %s > wall-clock cap %s", i, d, wallClock)
		}
	}
}

// TestRetryingSender_StopsOnContextCancel pins the ctx-aware
// behaviour: when the caller's context is cancelled mid-retry,
// Send returns ctx.Err() and stops sleeping.
func TestRetryingSender_StopsOnContextCancel(t *testing.T) {
	t.Parallel()
	te := &mail.TransientError{Status: 503}
	inner := &fakeSender{results: []error{te, te, te, te}}
	clk := &fakeClock{now: time.Unix(0, 0)}
	ctx, cancel := context.WithCancel(context.Background())
	r := &mail.RetryingSender{
		Inner:         inner,
		TransportName: mail.TransportResend,
		Log:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:           clk.Now,
		Sleep: func(_ context.Context, _ time.Duration) {
			// Cancel on first sleep so the second iteration sees
			// ctx.Err() before its second inner.Send.
			cancel()
		},
	}
	err := r.Send(ctx, mail.Message{})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

// TestRetryingSender_RecordsRetryMetric confirms each retry bumps
// the transport-labelled counter so the dashboards can graph
// upstream flakiness over time.
func TestRetryingSender_RecordsRetryMetric(t *testing.T) {
	t.Parallel()
	te := &mail.TransientError{Status: 503}
	inner := &fakeSender{results: []error{te, te, nil}}
	clk := &fakeClock{now: time.Unix(0, 0)}
	var got []string
	r := &mail.RetryingSender{
		Inner:         inner,
		TransportName: mail.TransportResend,
		Log:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:           clk.Now,
		Sleep:         clk.Sleep,
		Metrics:       recorderMetrics{onRetry: func(t string) { got = append(got, t) }},
	}
	if err := r.Send(context.Background(), mail.Message{}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if want := 2; len(got) != want {
		t.Errorf("retries recorded = %d, want %d", len(got), want)
	}
	for i, label := range got {
		if label != mail.TransportResend {
			t.Errorf("retries[%d] = %q, want %q", i, label, mail.TransportResend)
		}
	}
}

// TestRetryingSender_NilInnerReturnsError guards the obvious nil
// pointer so a misconfigured wiring panics visibly instead of
// silently dropping mail.
func TestRetryingSender_NilInnerReturnsError(t *testing.T) {
	t.Parallel()
	r := &mail.RetryingSender{
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if err := r.Send(context.Background(), mail.Message{}); err == nil {
		t.Fatal("expected error when Inner is nil")
	}
}

// TestBackoffDelay_BoundsAndGrows pins the full-jitter contract
// without leaking the unexported helper: each attempt's delay
// must be in [0, BaseDelay * 2^(attempt-1)) and zero for attempt 0.
func TestBackoffDelay_VisibleBehavior(t *testing.T) {
	t.Parallel()
	// We can't reach backoffDelay directly, so we exercise it
	// through a 3-attempt exhaustion and assert the *recorded*
	// sleeps stay within the documented bounds.
	base := time.Duration(api.MailRetryBaseDelayMS) * time.Millisecond
	te := &mail.TransientError{Status: 503}
	inner := &fakeSender{results: []error{te, te, te}}
	clk := &fakeClock{now: time.Unix(0, 0)}
	r := &mail.RetryingSender{
		Inner:         inner,
		TransportName: mail.TransportResend,
		Log:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:           clk.Now,
		Sleep:         clk.Sleep,
	}
	_ = r.Send(context.Background(), mail.Message{})
	sleeps := clk.Sleeps()
	if len(sleeps) != api.MailRetryMaxAttempts-1 {
		t.Fatalf("sleeps = %v, want %d entries", sleeps, api.MailRetryMaxAttempts-1)
	}
	for i, d := range sleeps {
		cap := base << i
		if d < 0 || d >= cap {
			t.Errorf("sleeps[%d] = %s, want in [0, %s)", i, d, cap)
		}
	}
}

// recorderMetrics satisfies mail.Metrics via two func fields.
type recorderMetrics struct {
	onFailure func(string)
	onRetry   func(string)
}

func (r recorderMetrics) RecordFailure(reason string) {
	if r.onFailure != nil {
		r.onFailure(reason)
	}
}

func (r recorderMetrics) RecordRetry(transport string) {
	if r.onRetry != nil {
		r.onRetry(transport)
	}
}
