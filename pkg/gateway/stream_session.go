package gateway

import (
	"context"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reqbudget"
)

// newStreamSession builds the context used by a response that may outlive
// the ordinary request budget. Before the response starts, the request
// budget (and the optional hop ceiling) still controls the session. Once the
// first response headers are committed, detach drops only that budget. The
// original request cancellation remains active, and the idle timer provides a
// separate resource-safety bound for a quiet stream.
func newStreamSession(parent context.Context, ceiling, idle time.Duration) (ctx context.Context, detach func(), touch func(), cancel context.CancelFunc) {
	budgetParent := parent
	budgetCancel := func() {}
	if b, ok := reqbudget.FromContext(parent); ok {
		if ceiling > 0 {
			budgetParent, budgetCancel, _ = b.WithCeiling(parent, ceiling)
		}
	} else if ceiling > 0 {
		budgetParent, budgetCancel = context.WithTimeout(parent, ceiling)
	}

	budgetCtx, detachBudget, cancelBudget := reqbudget.WithStream(budgetParent)
	ctx, cancelSession := context.WithCancel(budgetCtx)
	idleCtl := newIdleSession(ctx, idle)

	return idleCtl.ctx, detachBudget, idleCtl.touch, func() {
		idleCtl.stop()
		cancelSession()
		cancelBudget()
		budgetCancel()
	}
}

type idleSession struct {
	ctx    context.Context
	cancel context.CancelFunc
	reset  chan struct{}
	done   chan struct{}
	once   sync.Once
}

func newIdleSession(parent context.Context, idle time.Duration) *idleSession {
	if idle <= 0 {
		ctx, cancel := context.WithCancel(parent)
		return &idleSession{ctx: ctx, cancel: cancel, done: make(chan struct{})}
	}
	ctx, cancel := context.WithCancel(parent)
	s := &idleSession{
		ctx:    ctx,
		cancel: cancel,
		reset:  make(chan struct{}, 1),
		done:   make(chan struct{}),
	}
	go s.run(idle)
	return s
}

func (s *idleSession) run(idle time.Duration) {
	timer := time.NewTimer(idle)
	defer timer.Stop()
	defer close(s.done)
	for {
		select {
		case <-timer.C:
			s.cancel()
			return
		case <-s.reset:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(idle)
		case <-s.ctx.Done():
			return
		}
	}
}

func (s *idleSession) touch() {
	if s.reset == nil {
		return
	}
	select {
	case s.reset <- struct{}{}:
	default:
	}
}

func (s *idleSession) stop() {
	s.once.Do(func() {
		s.cancel()
		if s.reset != nil {
			<-s.done
		}
	})
}

// streamIdleTimeout is intentionally centralized so plain streaming and raw
// upgrades cannot drift into different idle semantics.
const streamIdleTimeout = api.StreamingIdleTimeoutDefault
