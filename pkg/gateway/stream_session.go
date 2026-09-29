package gateway

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reqbudget"
)

func isLongLivedForward(r *http.Request) bool {
	return r.Header.Get("x-faas-stream") == "true" || r.Header.Get("x-faas-protocol") == "grpc"
}

func newHTTPForwardSession(parent context.Context, longLived bool) (context.Context, func(), func(), context.CancelFunc) {
	if longLived {
		return newStreamSession(parent, api.HTTPForwardSessionTimeout, streamIdleTimeout)
	}
	ctx, touch, cancel := newOrdinaryResponseSession(parent, api.HTTPForwardSessionTimeout, streamIdleTimeout)
	return ctx, func() {}, touch, cancel
}

// newStreamSession builds the context used by a response that may outlive
// the ordinary request budget. Before the response starts, the request
// budget (and the optional hop ceiling) still controls the session. Once the
// first response headers are committed, detach drops only that budget. The
// original request cancellation remains active, and the idle timer provides a
// separate resource-safety bound for a quiet stream.
func newStreamSession(parent context.Context, ceiling, idle time.Duration) (ctx context.Context, detach func(), touch func(), cancel context.CancelFunc) {
	budgetCtx, detachBudget, cancelBudget := reqbudget.WithStream(parent)
	// gRPC serializes Deadline into a remote timer when opening an RPC. That
	// timer cannot be detached after response headers. Keep the handshake
	// budget in Done, and expose only the independent session ceiling.
	ctx, cancelSession := responseSessionContext(streamTransportContext{budgetCtx}, ceiling)
	idleCtl := newIdleSession(ctx, idle)

	return idleCtl.ctx, detachBudget, idleCtl.touch, func() {
		idleCtl.stop()
		cancelSession()
		cancelBudget()
	}
}

// streamTransportContext preserves cancellation and values while hiding the
// handshake deadline from transports that copy it into an independent timer.
type streamTransportContext struct{ context.Context }

func (streamTransportContext) Deadline() (time.Time, bool) { return time.Time{}, false }

func responseSessionContext(parent context.Context, ceiling time.Duration) (context.Context, context.CancelFunc) {
	if ceiling > 0 {
		return context.WithTimeout(parent, ceiling)
	}
	return context.WithCancel(parent)
}

// Ordinary responses retain the request deadline for their complete body.
func newOrdinaryResponseSession(parent context.Context, ceiling, idle time.Duration) (context.Context, func(), context.CancelFunc) {
	ctx, cancelSession := responseSessionContext(parent, ceiling)
	idleCtl := newIdleSession(ctx, idle)
	return idleCtl.ctx, idleCtl.touch, func() {
		idleCtl.stop()
		cancelSession()
	}
}

type idleSession struct {
	ctx          context.Context
	cancel       context.CancelFunc
	reset        chan struct{}
	done         chan struct{}
	once         sync.Once
	timedOutFlag atomic.Bool
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
			s.timedOutFlag.Store(true)
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

func (s *idleSession) timedOut() bool {
	return s != nil && s.timedOutFlag.Load()
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
