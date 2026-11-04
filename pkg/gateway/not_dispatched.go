package gateway

// adr: 20261022184531 — gatewayd-public retries responses that were never
// dispatched (step 5).
//
// production-us 2026-10-22: fsn-3 answered heartbeats but refused every wake.
// gatewayd-internal knows when a request failed before any byte reached a
// guest. It says so in a marker header, and the public edge may send such a
// request to another node. Nothing that reached customer code is retried, so
// a POST stays at-most-once.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"sync"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	// DispatchNonceHeader carries the public edge's per-request nonce to
	// gatewayd-internal. Like every x-faas-* request header it never reaches
	// a guest, on any gatewayd-internal release, so a guest cannot echo it.
	DispatchNonceHeader = "X-Faas-Dispatch-Nonce"
	// NotDispatchedHeader is gatewayd-internal's marker on a response whose
	// request reached no guest: "<reason>; nonce=<nonce>". The public edge
	// acts on it only when the nonce is the one it sent, and always strips
	// it; gatewayd-internal drops a guest's copy.
	NotDispatchedHeader = "X-Faas-Not-Dispatched"
)

// Reasons a response was not dispatched, the reason label of
// gateway_wake_failover_total.
const (
	// NotDispatchedWakeRefused: schedd refused the wake for a platform
	// reason (scan_critical, internal) that its own failover could not
	// place elsewhere.
	NotDispatchedWakeRefused = "wake_refused"
	// NotDispatchedWakeHoldExpired: the wake hold on this node expired with
	// no wake in progress.
	NotDispatchedWakeHoldExpired = "wake_hold_expired"
	// NotDispatchedProxyRefused: the forward was proven undelivered
	// (ADR-1133): the stream to the target's vmmd could not be opened, or
	// vmmd refused the instance before dialing its guest.
	NotDispatchedProxyRefused = "proxy_refused"
)

type dispatchNonceKey struct{}

// takeDispatchNonce moves the public edge's nonce from the request headers
// to the request context, so no later hop forwards it.
func takeDispatchNonce(r *http.Request) *http.Request {
	nonce := strings.TrimSpace(r.Header.Get(DispatchNonceHeader))
	r.Header.Del(DispatchNonceHeader)
	if nonce == "" {
		return r
	}
	return r.WithContext(context.WithValue(r.Context(), dispatchNonceKey{}, nonce))
}

// markNotDispatched stamps the marker on a response gatewayd-internal is
// about to write, when the public edge sent a nonce.
func markNotDispatched(w http.ResponseWriter, r *http.Request, reason string) {
	if r == nil || reason == "" {
		return
	}
	nonce, _ := r.Context().Value(dispatchNonceKey{}).(string)
	if nonce == "" {
		return
	}
	w.Header().Set(NotDispatchedHeader, reason+"; nonce="+nonce)
}

// wakeNotDispatchedReason classifies a wake error the gateway is about to
// answer with. Customer outcomes are never retried on another node: the
// app's own concurrency limits and queues, and an app that failed to start,
// would fail the same way there, and retrying them would only multiply the
// load. Fleet capacity is not retried either: every node asks the same
// schedd, and a retry adds load exactly when capacity is short. A wake still
// in progress is answered without a refusal.
func wakeNotDispatchedReason(err error) (string, bool) {
	switch {
	case err == nil:
		return "", false
	case errors.Is(err, ErrWakeQueueWaitTimeout):
		return NotDispatchedWakeHoldExpired, true
	}
	var prob *api.Problem
	if !errors.As(err, &prob) {
		return "", false
	}
	switch prob.Code {
	case api.CodeScanCritical, api.CodeInternal, "internal":
		return NotDispatchedWakeRefused, true
	}
	return "", false
}

// parseNotDispatched returns the reason of a marker carrying nonce.
func parseNotDispatched(value, nonce string) (string, bool) {
	if nonce == "" {
		return "", false
	}
	reason, rest, ok := strings.Cut(value, ";")
	if !ok {
		return "", false
	}
	got, ok := strings.CutPrefix(strings.TrimSpace(rest), "nonce=")
	if !ok || got != nonce {
		return "", false
	}
	switch reason = strings.TrimSpace(reason); reason {
	case NotDispatchedWakeRefused, NotDispatchedWakeHoldExpired, NotDispatchedProxyRefused:
		return reason, true
	}
	return "", false
}

func newDispatchNonce() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// DialRecord is the per-request state a node-choosing InternalDialer shares
// with the public edge: the nodes an earlier attempt reached, which a retry
// must avoid, and the node the latest attempt dialed. A dialer that cannot
// name the node it dialed leaves Dialed empty, and the edge then does not
// retry.
type DialRecord struct {
	mu       sync.Mutex
	excluded map[string]bool
	dialed   string
}

// NewDialRecord returns a record whose attempts avoid the excluded nodes.
func NewDialRecord(excluded ...string) *DialRecord {
	r := &DialRecord{excluded: make(map[string]bool, len(excluded))}
	for _, node := range excluded {
		r.excluded[node] = true
	}
	return r
}

// Dialed returns the node the latest attempt dialed, or "".
func (r *DialRecord) Dialed() string {
	if r == nil {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.dialed
}

type dialRecordKey struct{}

// WithDialRecord attaches rec to ctx for the dialer to read.
func WithDialRecord(ctx context.Context, rec *DialRecord) context.Context {
	return context.WithValue(ctx, dialRecordKey{}, rec)
}

// DialRecordFrom returns the record attached to ctx, or nil.
func DialRecordFrom(ctx context.Context) *DialRecord {
	rec, _ := ctx.Value(dialRecordKey{}).(*DialRecord)
	return rec
}

// Excluded reports whether a retry must avoid node. A nil record excludes
// nothing.
func (r *DialRecord) Excluded(node string) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.excluded[node]
}

// RecordDialed notes the node an attempt dialed.
func (r *DialRecord) RecordDialed(node string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.dialed = node
	r.mu.Unlock()
}

// excludeDialed moves the node the latest attempt dialed to the excluded
// set and returns it, or "" when the dialer did not name it.
func (r *DialRecord) excludeDialed() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	node := r.dialed
	r.dialed = ""
	if node == "" {
		return ""
	}
	if r.excluded == nil {
		r.excluded = map[string]bool{}
	}
	r.excluded[node] = true
	return node
}

// AlternateDialer is an InternalDialer that chooses among compute nodes and
// can say whether a node outside rec's excluded set is available.
type AlternateDialer interface {
	InternalDialer
	HasAlternate(ctx context.Context, rec *DialRecord) bool
}

// failoverBudget keeps retries within a share of requests: every request
// deposits pct/100 of a token, a retry spends one, and the bank holds at most
// burst tokens, so a quiet edge can still retry while a failing fleet cannot
// multiply its load. It counts in hundredths of a token, so ten deposits at
// 10 % make exactly one.
type failoverBudget struct {
	mu         sync.Mutex
	hundredths int
	pct        int
	burst      int
}

func newFailoverBudget(pct, burst int) *failoverBudget {
	return &failoverBudget{hundredths: burst * 100, pct: pct, burst: burst * 100}
}

func (b *failoverBudget) deposit() {
	b.mu.Lock()
	b.hundredths = min(b.burst, b.hundredths+b.pct)
	b.mu.Unlock()
}

func (b *failoverBudget) spend() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.hundredths < 100 {
		return false
	}
	b.hundredths -= 100
	return true
}
