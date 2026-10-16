package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Recovery retries observations, never mutations. Every call inherits the
// overall operation deadline, including time spent backing off.
const testCampaignRecoveryBudget = 2 * time.Minute

type testCampaignRecoveryPolicy struct {
	Timeout, Backoff, MaxBackoff time.Duration
	Attempts                     int
}
type testCampaignRecoveryPolicyKey struct{}

func testCampaignRecoveryIOPolicy(ctx context.Context) testCampaignRecoveryPolicy {
	if p, ok := ctx.Value(testCampaignRecoveryPolicyKey{}).(testCampaignRecoveryPolicy); ok {
		return p
	}
	return testCampaignRecoveryPolicy{Timeout: 5 * time.Second, Backoff: 250 * time.Millisecond, MaxBackoff: 2 * time.Second, Attempts: 3}
}

type testCampaignRecoveryIssue struct {
	Resource  string `json:"resource"`
	Operation string `json:"operation"`
	Reason    string `json:"reason"`
	Attempts  int    `json:"attempts"`
	Retryable bool   `json:"retryable"`
}

type testCampaignRecoveryCallError struct {
	cause     error
	reason    string
	attempts  int
	retryable bool
}

func (e *testCampaignRecoveryCallError) Error() string {
	return fmt.Sprintf("%s after %d lookup attempt(s); resource remains unverified", e.reason, e.attempts)
}
func (e *testCampaignRecoveryCallError) Unwrap() error { return e.cause }

func classifyTestCampaignRecoveryIOFailure(err error) (string, bool) {
	if errors.Is(err, context.Canceled) {
		return "operation_cancelled", false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "request_timeout", true
	}
	var problem *api.APIError
	if errors.As(err, &problem) {
		switch problem.Problem.Status {
		case 429:
			return "rate_limited", true
		case 408, 500, 502, 503, 504:
			return "api_unavailable", true
		default:
			return "verification_required", false
		}
	}
	var n net.Error
	if errors.As(err, &n) && (n.Timeout() || n.Temporary()) {
		return "connection_unavailable", true
	}
	if errors.Is(err, net.ErrClosed) || errors.Is(err, syscall.ENETUNREACH) || errors.Is(err, syscall.EHOSTUNREACH) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) {
		return "connection_unavailable", true
	}
	var truncated *api.ResponseTruncatedError
	if errors.As(err, &truncated) {
		return "connection_unavailable", true
	}
	return "verification_required", false
}

func testCampaignRecoveryRetryAfter(err error) time.Duration {
	var problem *api.APIError
	if !errors.As(err, &problem) {
		return 0
	}
	if s := problem.Problem.RetryAfterSeconds; s != nil && *s > 0 {
		// Avoid duration overflow on untrusted Retry-After values.
		if *s > 86400 {
			return 24 * time.Hour
		}
		return time.Duration(*s) * time.Second
	}
	for _, v := range problem.Problem.HasHeader("Retry-After") {
		if date, err := http.ParseTime(v); err == nil && date.After(time.Now()) {
			return time.Until(date)
		}
	}
	return 0
}

func testCampaignRecoveryRead[T any](ctx context.Context, call func(context.Context) (T, error)) (T, error) {
	p := testCampaignRecoveryIOPolicy(ctx)
	var zero T
	for attempt := 1; attempt <= p.Attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return zero, testCampaignRecoveryContextError(err, attempt-1)
		}
		request, cancel := context.WithTimeout(ctx, p.Timeout)
		value, err := call(request)
		timeoutErr := request.Err()
		cancel()
		if err == nil && timeoutErr != nil {
			err = timeoutErr
		}
		if err == nil {
			return value, nil
		}
		if ctx.Err() != nil {
			return zero, testCampaignRecoveryContextError(ctx.Err(), attempt)
		}
		reason, retry := classifyTestCampaignRecoveryIOFailure(err)
		failure := &testCampaignRecoveryCallError{err, reason, attempt, retry}
		if !retry || attempt == p.Attempts || ctx.Err() != nil {
			return zero, failure
		}
		delay := p.Backoff * time.Duration(1<<(attempt-1))
		if delay > p.MaxBackoff {
			delay = p.MaxBackoff
		}
		if after := testCampaignRecoveryRetryAfter(err); after > delay {
			delay = after
		}
		// Do not retry sooner than a server asks, or exceed our local wait budget.
		if delay > p.MaxBackoff {
			return zero, failure
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return zero, testCampaignRecoveryContextError(ctx.Err(), attempt)
		case <-timer.C:
		}
	}
	return zero, errors.New("recovery lookup policy has no attempts")
}

func testCampaignRecoveryIssueFor(resource, operation string, err error) *testCampaignRecoveryIssue {
	reason, retry := classifyTestCampaignRecoveryIOFailure(err)
	issue := &testCampaignRecoveryIssue{Resource: resource, Operation: operation, Reason: reason, Retryable: retry}
	var call *testCampaignRecoveryCallError
	if errors.As(err, &call) {
		issue.Reason = call.reason
		issue.Attempts = call.attempts
		issue.Retryable = call.retryable
	} else if retry {
		issue.Attempts = 1
	}
	return issue
}

type testCampaignRegistrationReader interface {
	GetScenarioTestRegistration(context.Context, string) (api.ScenarioTestRegistration, error)
}

func readTestCampaignRecoveryRegistration(ctx context.Context, c testCampaignRegistrationReader, run string) (api.ScenarioTestRegistration, error) {
	return testCampaignRecoveryRead(ctx, func(request context.Context) (api.ScenarioTestRegistration, error) {
		return c.GetScenarioTestRegistration(request, run)
	})
}
func readTestCampaignRecoverySession(ctx context.Context, c testCampaignCleanupReader, project, run string) (api.DevSessionResponse, error) {
	return testCampaignRecoveryRead(ctx, func(request context.Context) (api.DevSessionResponse, error) {
		return c.GetDevSession(request, project, run)
	})
}
func deleteTestCampaignRecoveryRegistration(ctx context.Context, c testCampaignFaultClient, run, digest string) error {
	request, cancel := context.WithTimeout(ctx, testCampaignRecoveryIOPolicy(ctx).Timeout)
	defer cancel()
	if err := request.Err(); err != nil {
		return err
	}
	return c.DeleteScenarioTestRegistration(request, run, digest)
}
func deleteTestCampaignRecoverySession(ctx context.Context, c testCampaignCleanupClient, project, run string) error {
	request, cancel := context.WithTimeout(ctx, testCampaignRecoveryIOPolicy(ctx).Timeout)
	defer cancel()
	if err := request.Err(); err != nil {
		return err
	}
	return c.DestroyDevSession(request, project, run)
}

func testCampaignRecoveryContextError(err error, attempts int) error {
	reason := "operation_deadline"
	if errors.Is(err, context.Canceled) {
		reason = "operation_cancelled"
	}
	return &testCampaignRecoveryCallError{err, reason, attempts, errors.Is(err, context.DeadlineExceeded)}
}
