// adr: 712
package durableentity

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func invocationLeaseOptions(opts *Options) error {
	if opts.InvocationLeaseDuration < 0 || opts.InvocationLeaseDuration > api.MaxDurableEntityLease || opts.InvocationRenewInterval < 0 {
		return ErrInvalid
	}
	if opts.InvocationLeaseDuration == 0 {
		if opts.InvocationRenewInterval != 0 {
			return ErrInvalid
		}
		return nil
	}
	if opts.InvocationRenewInterval == 0 {
		opts.InvocationRenewInterval = opts.InvocationLeaseDuration / 3
	}
	if opts.InvocationRenewInterval <= 0 || opts.InvocationRenewInterval >= opts.InvocationLeaseDuration/2 || opts.InvocationLeaseDuration/6 == 0 {
		return ErrInvalid
	}
	return nil
}

func (m *Manager) executeInvocation(ctx context.Context, claim Claim, request Request, handler func(context.Context, View) (Transition, error)) (Result, error) {
	if m.invocationLease == 0 {
		return m.Execute(ctx, claim, request, handler)
	}
	ctx, deadline := context.WithTimeout(ctx, api.DurableEntityInvokeTimeout)
	defer deadline()
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	renewCtx, stop := context.WithCancel(ctx)
	var publication sync.Mutex
	done := make(chan struct{})
	go func() {
		defer close(done)
		m.keepInvocationLease(renewCtx, claim, &publication, cancel)
	}()
	result, err := m.execute(ctx, claim, request, handler, &publication)
	stop()
	<-done // No renewal may race the caller's independent release cleanup.
	if err != nil && ctx.Err() != nil {
		err = errors.Join(err, context.Cause(ctx))
	}
	// A later renewal or cancellation cannot invalidate acknowledged state.
	return result, err
}

func (m *Manager) keepInvocationLease(ctx context.Context, claim Claim, publication *sync.Mutex, cancel context.CancelCauseFunc) {
	timer := time.NewTimer(m.invocationRenewDelay(claim))
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		publication.Lock()
		if ctx.Err() != nil {
			publication.Unlock()
			return
		}
		next, err := m.renewInvocationLease(ctx, claim)
		if err != nil && ctx.Err() == nil {
			// Cancel while holding the publication lock: the final CAS cannot
			// slip between a failed renewal and cancellation.
			cancel(err)
		}
		publication.Unlock()
		if err != nil {
			return
		}
		claim = next
		timer.Reset(m.invocationRenewDelay(claim))
	}
}

func (m *Manager) invocationRenewDelay(claim Claim) time.Duration {
	remaining := claim.ExpiresAt.Sub(m.now())
	budget := min(api.DurableEntityRenewTimeout, m.invocationLease/6)
	return max(0, min(m.renewInterval, (remaining-budget)/2))
}

func (m *Manager) renewInvocationLease(ctx context.Context, claim Claim) (Claim, error) {
	budget := min(api.DurableEntityRenewTimeout, m.invocationLease/6, claim.ExpiresAt.Sub(m.now()))
	if budget <= 0 {
		return Claim{}, ErrStaleOwner
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	for {
		next, err := m.renew(ctx, claim, m.invocationLease)
		if !errors.Is(err, ErrConflict) {
			return next, err
		}
		// A definite CAS rejection may be retried after a fresh read. An
		// uncertain dispatched write cancels work; it is never retried here.
		timer := time.NewTimer(api.DurableEntityRenewRetryInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return Claim{}, errors.Join(ErrConflict, ctx.Err())
		case <-timer.C:
		}
	}
}
