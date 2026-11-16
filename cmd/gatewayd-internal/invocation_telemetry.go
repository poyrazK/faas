package main

import (
	"net/http"
	"sync/atomic"
	"time"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// Scheduled and queued invocations (cron, queue, async invoke, delayed
// tasks) reach the guest through synthAdapter, which calls the forwarder
// directly and never passes the public handler's observe. A cron failing
// every 15 minutes for weeks was missing from request metrics, HTTP logs and
// telemetry (prod findings 2026-11-15 #7). Each delivery is now recorded the
// way an internal service call is (gateway.Handler.RecordServiceRequest):
// request-duration metrics and a telemetry row, with no financial usage
// event, since delivery is the platform's work, not ingress.

// invocationRequestObserver hands the public handler's recorder to the synth
// adapter, which runOnPool builds before runWithDeps builds the handler.
type invocationRequestObserver struct {
	fn atomic.Pointer[func(*http.Request, gateway.ServiceRequestObservation)]
}

func (o *invocationRequestObserver) set(fn func(*http.Request, gateway.ServiceRequestObservation)) {
	if o != nil && fn != nil {
		o.fn.Store(&fn)
	}
}

func (o *invocationRequestObserver) observe(r *http.Request, obs gateway.ServiceRequestObservation) {
	if o == nil {
		return
	}
	if fn := o.fn.Load(); fn != nil {
		(*fn)(r, obs)
	}
}

// observeInvocation records one delivered invocation. A delivery that got no
// HTTP status (transport error, abort) is not a request and is left to the
// invocation ledger.
func (a *synthAdapter) observeInvocation(req *http.Request, target gateway.Target, inv state.Invocation, started time.Time, status int) {
	if a.requestObserver == nil || status < 100 || status > 599 {
		return
	}
	a.requestObserver.observe(req, gateway.ServiceRequestObservation{
		AccountID:        inv.AccountID,
		Target:           target,
		ReceivedAt:       started,
		LatencyMS:        int(max(time.Since(started), 0) / time.Millisecond),
		Status:           status,
		InvocationSource: string(inv.Source),
	})
}
