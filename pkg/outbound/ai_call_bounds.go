package outbound

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/onebox-faas/faas/pkg/api"
)

// aiCallContext bounds a billable AI call (ADR 20261027132936): the
// integration's request timeout bounds the wait for the response headers,
// and the call ends at most api.OutboundAICallMaxSeconds after it starts.
// When the header wait or a stream's idle bound runs out, Err reports
// context.DeadlineExceeded, so retries and the circuit breaker see a timeout
// as they did when the request timeout bounded the whole call.
type aiCallContext struct {
	context.Context
	cancel      context.CancelCauseFunc
	cancelCall  context.CancelFunc
	headerTimer *time.Timer
}

// newAICallContext starts a call's bounds. Close releases them.
func newAICallContext(parent context.Context, headerTimeout time.Duration, started time.Time) *aiCallContext {
	call, cancelCall := context.WithDeadline(parent, started.Add(api.OutboundAICallMaxSeconds*time.Second))
	inner, cancel := context.WithCancelCause(call)
	c := &aiCallContext{Context: inner, cancel: cancel, cancelCall: cancelCall}
	if headerTimeout > 0 {
		c.headerTimer = time.AfterFunc(headerTimeout, c.expire)
	}
	return c
}

// Err reports a bound that ran out as a deadline.
func (c *aiCallContext) Err() error {
	err := c.Context.Err()
	if err != nil && errors.Is(context.Cause(c.Context), context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return err
}

// expire ends the call on one of its bounds.
func (c *aiCallContext) expire() { c.cancel(context.DeadlineExceeded) }

// headersArrived stops the header bound; the call runs on to its end.
func (c *aiCallContext) headersArrived() {
	if c.headerTimer != nil {
		c.headerTimer.Stop()
	}
}

// Close releases the call's timers and contexts.
func (c *aiCallContext) Close() {
	c.headersArrived()
	c.cancel(nil)
	c.cancelCall()
}

// idleBoundBody ends a streamed answer that sends no byte for the idle bound.
type idleBoundBody struct {
	io.ReadCloser
	timer *time.Timer
	idle  time.Duration
}

// boundStreamIdle wraps a streamed body so a silent provider ends the call.
func boundStreamIdle(body io.ReadCloser, call *aiCallContext, idle time.Duration) io.ReadCloser {
	return &idleBoundBody{ReadCloser: body, timer: time.AfterFunc(idle, call.expire), idle: idle}
}

func (b *idleBoundBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		b.timer.Reset(b.idle)
	}
	return n, err
}

func (b *idleBoundBody) Close() error {
	b.timer.Stop()
	return b.ReadCloser.Close()
}

// LeaseRenewer extends a live admission lease (ADR 20261027132936), so a
// long AI call keeps its reservation and in-flight slot until it settles.
type LeaseRenewer interface {
	RenewLease(ctx context.Context, integrationID, leaseID string, ttl time.Duration) error
}

// aiStreamIdleBound and aiLeaseRenewMin are the stream idle bound and the
// shortest lease renewal span; tests shorten them.
var (
	aiStreamIdleBound = api.OutboundAIStreamIdleSeconds * time.Second
	aiLeaseRenewMin   = api.OutboundAILeaseRenewMinSeconds * time.Second
)

// aiLeaseRenewalSpan is how far ahead each renewal moves a lease.
func aiLeaseRenewalSpan(requestTimeout time.Duration) time.Duration {
	return max(requestTimeout, aiLeaseRenewMin)
}

// renewAILease renews a call's lease every third of its renewal span until
// the returned stop is called. A failed renewal is retried on the next tick;
// the lease then expires as it did before renewals existed.
func (h *Handler) renewAILease(integrationID, leaseID string, requestTimeout time.Duration) (stop func()) {
	renewer, ok := h.Backend.(LeaseRenewer)
	if !ok || leaseID == "" {
		return func() {}
	}
	span := aiLeaseRenewalSpan(requestTimeout)
	ticker := time.NewTicker(span / 3)
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				renewCtx, cancel := context.WithTimeout(context.Background(), span/3)
				_ = renewer.RenewLease(renewCtx, integrationID, leaseID, span)
				cancel()
			}
		}
	}()
	return func() {
		ticker.Stop()
		close(done)
		wg.Wait()
	}
}

// unansweredAIStatus is the request-log status of a call that ended before
// its response headers: the gateway's deadline, or the caller leaving.
func unansweredAIStatus(err error) (int, bool) {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout, true
	case errors.Is(err, context.Canceled):
		return 499, true
	default:
		return 0, false
	}
}

// settleUnansweredAICall settles a billable call whose request reached the
// provider but whose response never came back: the provider may still run
// and bill it, so it counts at its reservation as unknown usage (ADR-996,
// ADR 20261027132936).
func (h *Handler) settleUnansweredAICall(ctx context.Context, integration Integration, appID string, settlement aiSettlement,
	span oteltrace.Span, metricIntegrationID string) {
	if !settlement.billable || (settlement.reserve.tokens <= 0 && settlement.reserve.cost <= 0) {
		return
	}
	usage := settlement.reserve.estimate
	usage.Model, usage.Known = settlement.requestModel, false
	span.SetAttributes(attribute.Bool("gregale.ai.usage_estimated", true))
	h.recordSettledAIUsage(ctx, integration, appID, settlement,
		settledAIUsage{usage: usage, outcome: AIUsageOutcomeUnknown, cost: settlement.reserve.cost}, span, metricIntegrationID)
}
