package gateway

// adr: 20261022184531 — gatewayd-public retries responses that were never
// dispatched (step 5).

import (
	"bytes"
	"io"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reqbudget"
)

// Outcomes of gateway_wake_failover_total. A retry ends in succeeded (the
// other node dispatched the request), refused_again or failed (the transport
// failed and the edge answers with the refusal it holds). A refusal the edge
// does not retry names why.
const (
	FailoverOutcomeSucceeded     = "succeeded"
	FailoverOutcomeRefusedAgain  = "refused_again"
	FailoverOutcomeFailed        = "failed"
	FailoverOutcomeMaxNodes      = "max_nodes"
	FailoverOutcomeBodyNotReplay = "body_not_replayable"
	FailoverOutcomeOutOfTime     = "out_of_time"
	FailoverOutcomeNodeUnknown   = "node_unknown"
	FailoverOutcomeNoAlternate   = "no_alternate"
	FailoverOutcomeNoBudget      = "no_budget"
	FailoverOutcomeRefusalLarge  = "refusal_too_large"
)

// FailoverOutcomes lists every outcome, for metric pre-registration.
var FailoverOutcomes = []string{
	FailoverOutcomeSucceeded, FailoverOutcomeRefusedAgain, FailoverOutcomeFailed,
	FailoverOutcomeMaxNodes, FailoverOutcomeBodyNotReplay, FailoverOutcomeOutOfTime,
	FailoverOutcomeNodeUnknown, FailoverOutcomeNoAlternate, FailoverOutcomeNoBudget,
	FailoverOutcomeRefusalLarge,
}

// NotDispatchedReasons lists every reason, for metric pre-registration.
var NotDispatchedReasons = []string{NotDispatchedWakeRefused, NotDispatchedWakeHoldExpired, NotDispatchedProxyRefused}

// WakeFailover is gatewayd-public's retry of a response gatewayd-internal
// marked not dispatched. It needs a node-choosing dialer (AlternateDialer);
// with any other dialer the proxy forwards as before.
type WakeFailover struct {
	budget  *failoverBudget
	observe func(reason, outcome string)
	now     func() time.Time
}

// NewWakeFailover returns the edge's failover with its retry budget. observe
// counts each retried or unretried refusal and may be nil.
func NewWakeFailover(observe func(reason, outcome string)) *WakeFailover {
	if observe == nil {
		observe = func(string, string) {}
	}
	return &WakeFailover{
		budget:  newFailoverBudget(api.GatewayFailoverRetryBudgetPct, api.GatewayFailoverRetryBudgetBurst),
		observe: observe,
		now:     time.Now,
	}
}

// WithWakeFailover enables the not-dispatched retry. Returns the proxy for
// fluent chaining.
func (p *InternalReverseProxy) WithWakeFailover(f *WakeFailover) *InternalReverseProxy {
	p.WakeFailover = f
	return p
}

// roundTrip forwards outReq. With failover enabled it sends a fresh nonce,
// and while gatewayd-internal answers that the request was not dispatched it
// tries another compute node, at most api.GatewayFailoverMaxExtraNodes of
// them. The marker never reaches the client.
func (p *InternalReverseProxy) roundTrip(outReq *http.Request, duplex bool) (*http.Response, error) {
	f := p.WakeFailover
	alternates, ok := p.Dialer.(AlternateDialer)
	var nonce string
	if f != nil && ok && !duplex {
		nonce, _ = newDispatchNonce()
	}
	if nonce == "" {
		// A client cannot ask gatewayd-internal for a marker.
		outReq.Header.Del(DispatchNonceHeader)
		resp, err := p.Transport.RoundTrip(outReq)
		stripNotDispatched(resp)
		return resp, err
	}
	f.budget.deposit()
	outReq.Header.Set(DispatchNonceHeader, nonce)
	rec := &DialRecord{}
	attempt := outReq.WithContext(WithDialRecord(outReq.Context(), rec))
	resp, err := p.Transport.RoundTrip(attempt)
	var held *http.Response
	pending := ""
	for extra := 0; ; extra++ {
		if err != nil {
			if held == nil {
				return nil, err
			}
			f.observe(pending, FailoverOutcomeFailed)
			return held, nil
		}
		reason, marked := parseNotDispatched(resp.Header.Get(NotDispatchedHeader), nonce)
		resp.Header.Del(NotDispatchedHeader)
		if pending != "" {
			if marked {
				f.observe(pending, FailoverOutcomeRefusedAgain)
			} else {
				f.observe(pending, FailoverOutcomeSucceeded)
			}
			pending = ""
		}
		if !marked {
			return resp, nil
		}
		if skip := f.skip(outReq, rec, alternates, extra); skip != "" {
			f.observe(reason, skip)
			return resp, nil
		}
		if held, ok = holdRefusal(resp); !ok {
			f.observe(reason, FailoverOutcomeRefusalLarge)
			return held, nil
		}
		retry, cloneErr := failoverRetryRequest(outReq, rec)
		if cloneErr != nil {
			f.observe(reason, FailoverOutcomeBodyNotReplay)
			return held, nil
		}
		p.logger().Info("gatewayd-public: request not dispatched; retrying on another compute node",
			"reason", reason, "excluded", extra+1)
		pending = reason
		resp, err = p.Transport.RoundTrip(retry) //nolint:bodyclose // returned to serveHTTP, which owns the body.
	}
}

// skip returns why a not-dispatched response is not retried, or "". The node
// the refusal came from is excluded first, so availability is judged without
// it, and the budget is spent last, only on a retry that happens.
func (f *WakeFailover) skip(req *http.Request, rec *DialRecord, alternates AlternateDialer, extra int) string {
	switch {
	case extra >= api.GatewayFailoverMaxExtraNodes:
		return FailoverOutcomeMaxNodes
	case req.Body != nil && req.GetBody == nil:
		// The body streamed to the first node and cannot be sent again.
		return FailoverOutcomeBodyNotReplay
	case req.ContentLength > api.GatewayFailoverMaxBufferedBodyBytes:
		return FailoverOutcomeBodyNotReplay
	case !f.timeLeft(req):
		return FailoverOutcomeOutOfTime
	case rec.excludeDialed() == "":
		return FailoverOutcomeNodeUnknown
	case !alternates.HasAlternate(req.Context(), rec):
		return FailoverOutcomeNoAlternate
	case !f.budget.spend():
		return FailoverOutcomeNoBudget
	}
	return ""
}

// timeLeft reports whether enough of the request's budget remains for
// another node's wake: the same floor schedd keeps for its own alternate
// attempts.
func (f *WakeFailover) timeLeft(req *http.Request) bool {
	if req.Context().Err() != nil {
		return false
	}
	b, ok := reqbudget.FromContext(req.Context())
	if !ok {
		return true
	}
	return b.Remaining(f.now()) >= time.Duration(api.WakeFailoverMinRemainingMs)*time.Millisecond
}

// holdRefusal reads a refusal's body into memory, so the edge can still
// answer with it when every retry fails. It reports false, with the body
// restored, when the refusal is larger than api.GatewayFailoverMaxRefusalBodyBytes.
func holdRefusal(resp *http.Response) (*http.Response, bool) {
	body, err := io.ReadAll(io.LimitReader(resp.Body, api.GatewayFailoverMaxRefusalBodyBytes+1))
	if err != nil || len(body) > api.GatewayFailoverMaxRefusalBodyBytes {
		resp.Body = struct {
			io.Reader
			io.Closer
		}{io.MultiReader(bytes.NewReader(body), resp.Body), resp.Body}
		return resp, false
	}
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	return resp, true
}

// failoverRetryRequest clones the forwarded request for another node, with a fresh
// copy of its buffered body and the same dial record.
func failoverRetryRequest(outReq *http.Request, rec *DialRecord) (*http.Request, error) {
	retry := outReq.Clone(WithDialRecord(outReq.Context(), rec))
	if outReq.GetBody != nil {
		body, err := outReq.GetBody()
		if err != nil {
			return nil, err
		}
		retry.Body = body
	}
	return retry, nil
}

// stripNotDispatched removes a marker the edge did not ask for.
func stripNotDispatched(resp *http.Response) {
	if resp != nil {
		resp.Header.Del(NotDispatchedHeader)
	}
}
