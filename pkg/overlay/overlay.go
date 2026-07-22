// Package overlay is the cross-box dial abstraction for issue #98 /
// ADR-028 / issue #120. vmmd's gRPC listener is the only thing
// gatewayd (or schedd's heartbeat goroutine) dials over the overlay;
// the dial itself is just a thin wrapper around pkg/wire.DialContext
// with the per-node target resolved from the compute_nodes row.
//
// Why a separate package from pkg/wire: wire is the
// location-transparent dial helper (issue #95 — unix/tcp/dns
// targets, mTLS). Overlay is the per-compute-node helper that
// resolves a compute_node.id → its target_url → a dialed
// *grpc.ClientConn. Splitting it lets schedd's heartbeat goroutine
// dial every active node without caring whether each one is unix
// (default-local), tcp (overlay), or dns (future edge).
//
// This package intentionally exposes only two things:
//
//   - New(raw string) Target: wrap a target string in a typed handle
//     callers can pass into dial loops without re-parsing. tlsCfg is
//     NOT carried here because a Target is meant to be cacheable per
//     node; the TLS material is dial-time.
//   - Dial(ctx, target Target, tlsCfg *tls.Config) (*grpc.ClientConn,
//     error): the dial itself. Returns the wire dial error verbatim
//     — the heartbeat goroutine's only job is to map non-nil errors
//     to "this node is sick" and call SetComputeNodeActive(ctx, id, false).
//
// No caching: the heartbeat goroutine pings each node on a 30s
// cadence, and the per-node cost is dominated by mTLS + RTT (a few
// ms on Tailscale). Caching the conn would let a stale conn look
// healthy right when the heartbeat should be reporting failure;
// the design choice is "every heartbeat pays the dial cost and
// sees the truth". The gateway-side cache (pkg/gateway.NodeClientCache)
// is a different code path that DOES cache, because its hot path
// serves every customer request and the cost trade-off is
// different.
//
// Production callers (issue #120):
//
//   - cmd/gatewayd/nodecache.go: per-node dial closure inside
//     NodeClientCache. The cache itself is unrelated to this
//     package; only the underlying wire.DialContext call is swapped.
//   - pkg/sched/vmmclient.go: DialVMMContext — schedd's per-node
//     router dial goes through here.
//   - pkg/sched/heartbeat.go (via cmd/schedd/heartbeat_dialer.go):
//     heartbeat goroutine dials fresh per tick to honour the
//     "every heartbeat pays the dial cost" design intent.

package overlay

import (
	"context"
	"crypto/tls"

	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
)

// Target is a parsed compute_nodes.target_url — the destination
// gatewayd / schedd dials. The wrapper exists so callers can't pass
// a raw string + a raw TLS config together and accidentally mismatch
// them; the only construction path is New, which does no validation
// (the wire layer's ParseTarget covers that).
type Target struct {
	raw string
}

// New wraps a target_url into a typed handle. Empty input is
// allowed (returns a zero Target whose Dial fails fast) so the
// gateway's per-node client cache can register a placeholder for
// "node exists but its target is not yet known".
func New(raw string) Target {
	return Target{raw: raw}
}

// Raw returns the underlying wire target string. Used by tests and
// by the gateway's per-node client cache that builds its dial key
// from the raw form.
func (t Target) Raw() string { return t.raw }

// Dial opens a gRPC connection to the target. tlsCfg is required
// for tcp/dns targets (issue #95 / ADR-025); nil is fine for unix
// (single-box dev). The returned *grpc.ClientConn has the standard
// gRPC lazy-dial semantics — first RPC triggers the actual TCP
// dial — so a long-running heartbeat loop can construct the conn
// once and re-use it across ticks.
//
// Returns the wire dial error verbatim: a ctx deadline surfaces as
// context.DeadlineExceeded, a refused TCP as a connection error.
// The caller (schedd's heartbeat goroutine) treats any non-nil as
// "node is sick" and acts on it via state.SetComputeNodeActive.
func Dial(ctx context.Context, t Target, tlsCfg *tls.Config) (*grpc.ClientConn, error) {
	if t.raw == "" {
		return nil, ErrEmptyTarget
	}
	return wire.DialContext(ctx, t.raw, tlsCfg)
}

// ErrEmptyTarget is the sentinel for "Target{} constructed with no
// raw string". Exported so callers (e.g. schedd's heartbeat
// goroutine) can `errors.Is(err, overlay.ErrEmptyTarget)` to
// distinguish "config bug — the target URL was never set" from
// "remote node down" without string-matching. The address is
// stable for the lifetime of the process; package code MUST NOT
// construct additional values of *OverlayError with the same
// sentinel — guard at the call site, not via constructor identity.
var ErrEmptyTarget = &OverlayError{msg: "overlay: empty target_url"}

// OverlayError wraps an overlay-package-level error. Distinct
// from grpc / wire errors so a `errors.Is(err, overlay.ErrEmptyTarget)`
// (or `errors.As(err, &OverlayError{})`) check is the heartbeat
// goroutine's way of distinguishing "config bug" from "remote
// node down". The package only ever returns one sentinel value
// of this type (ErrEmptyTarget); future error kinds should grow
// additional exported sentinels in this same shape.
type OverlayError struct {
	msg string
}

func (e *OverlayError) Error() string { return e.msg }
