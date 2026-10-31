// adr: 570
package gateway

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/realtime"
	"github.com/onebox-faas/faas/pkg/reqbudget"
	"github.com/onebox-faas/faas/pkg/trafficrevocation"
)

const (
	trafficSecurityHeader   = "X-Faas-Traffic-Security"
	trafficSecurityRealtime = "v1;managed-realtime"
	// trafficSecurityFenceOff marks an answer an operator chose to serve
	// without the revocation fence (platformflags.TrafficRevocationFence).
	trafficSecurityFenceOff = "v1;fence-off"
)

func encodeTrafficSecurity(states map[trafficrevocation.Scope]trafficrevocation.State) (string, error) {
	return trafficrevocation.EncodeSnapshot(states)
}

func decodeTrafficSecurity(value string) (map[trafficrevocation.Scope]trafficrevocation.State, error) {
	return trafficrevocation.DecodeSnapshot(value)
}

func stampTrafficSecurity(ctx context.Context, header http.Header) {
	header.Del(trafficSecurityHeader)
	if trafficFenceIsOff(ctx) {
		header.Set(trafficSecurityHeader, trafficSecurityFenceOff)
		return
	}
	// Invalid owner IDs can occur only in legacy fixtures. A production public
	// gateway refuses a successful response without this verified snapshot.
	if value, err := encodeTrafficSecurity(trafficSecuritySnapshot(ctx)); err == nil {
		header.Set(trafficSecurityHeader, value)
	}
}

type publicTrafficSecurityKey struct{}

type publicTrafficSecurity struct {
	registry *trafficrevocation.Registry
	cancel   context.CancelCauseFunc
	release  func()
	// peers is non-nil only while a compute gateway behind this edge predates
	// the snapshot header (release N-1); answers then pass unverified.
	peers TrafficSecurityPeers
	// observer counts answers passed unverified, including fence-off ones
	// while the snapshot is otherwise required.
	observer TrafficSecurityPeers
}

// TrafficSecurityPeers reports whether every compute gateway an edge may dial
// stamps the traffic security snapshot, and counts answers the edge forwards
// without verifying one.
type TrafficSecurityPeers interface {
	Required() bool
	Unverified()
}

func (p *InternalReverseProxy) WithTrafficRevocations(registry *trafficrevocation.Registry) *InternalReverseProxy {
	p.trafficRevocations = registry
	return p
}

// WithTrafficSecurityPeers makes the snapshot header mandatory only while
// peers reports that every compute gateway stamps it. Release N of the public
// edge runs beside N-1 compute gateways during a rollout
// (docs/release-compatibility.md); refusing their unstamped answers turned
// every successful response into a 503 (2026-10-26, rc.295, 10:39–12:14 UTC).
func (p *InternalReverseProxy) WithTrafficSecurityPeers(peers TrafficSecurityPeers) *InternalReverseProxy {
	p.trafficSecurityPeers = peers
	return p
}

func (p *InternalReverseProxy) publicTrafficContext(parent context.Context) (context.Context, func()) {
	if p.trafficRevocations == nil { // Legacy fixtures only; production wires it.
		return parent, func() {}
	}
	ctx, cancel := reqbudget.WithCancellationFence(parent)
	owner := &publicTrafficSecurity{registry: p.trafficRevocations, cancel: cancel, observer: p.trafficSecurityPeers}
	if p.trafficSecurityPeers != nil && !p.trafficSecurityPeers.Required() {
		owner.peers = p.trafficSecurityPeers
	}
	ctx = context.WithValue(ctx, publicTrafficSecurityKey{}, owner)
	return ctx, func() {
		if owner.release != nil {
			owner.release()
		}
		cancel(nil)
	}
}

// isUnverifiedPeerStream reports a stream from an N-1 compute gateway. That
// release marks a stream only with Streaming-Status, the rule its own edge
// used, and sends none of the response controls (session, deadline) an N
// gateway writes. Only an answer this edge forwards unverified, while a gateway
// it may dial predates the snapshot, may use the old rule; otherwise the edge
// cut every SSE answer from that node at the request budget.
func isUnverifiedPeerStream(r *http.Request, resp *http.Response) bool {
	owner, _ := r.Context().Value(publicTrafficSecurityKey{}).(*publicTrafficSecurity)
	if owner == nil || owner.peers == nil ||
		len(resp.Header.Values(trafficResponseSessionHeader)) != 0 || len(resp.Header.Values(trafficResponseDeadlineHeader)) != 0 {
		return false
	}
	if resp.StatusCode != http.StatusSwitchingProtocols && (resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusBadRequest) {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(resp.Header.Get(api.StreamingStatusHeader))) {
	case string(api.StreamingStatusStreaming), string(api.StreamingStatusUpgradeBypass):
		return true
	default:
		return false
	}
}

func bindPublicTrafficSecurity(r *http.Request, resp *http.Response) error {
	values := resp.Header.Values(trafficSecurityHeader)
	resp.Header.Del(trafficSecurityHeader)
	stripTrafficControlTrailerDeclarations(resp.Header)
	stripTrafficControlTrailers(resp.Trailer)
	owner, _ := r.Context().Value(publicTrafficSecurityKey{}).(*publicTrafficSecurity)
	if owner == nil {
		return nil
	}
	if len(values) == 0 && resp.StatusCode >= http.StatusBadRequest && !isLongLivedResponse(resp.StatusCode, resp.Header) {
		return nil // No owner exists for a pre-admission platform error.
	}
	if owner.peers != nil {
		// An N-1 gateway stamps nothing and does not strip a guest's copy, so
		// any value here is untrusted: drop it and forward as N-1 did.
		owner.peers.Unverified()
		return nil
	}
	if len(values) != 1 {
		return trafficrevocation.ErrUnavailable
	}
	if values[0] == trafficSecurityFenceOff {
		// The compute gateway is the fence's owner and strips any guest copy,
		// so its marker is trusted: the operator turned the fence off.
		if owner.observer != nil {
			owner.observer.Unverified()
		}
		return nil
	}
	if values[0] == trafficSecurityRealtime {
		if strings.HasPrefix(r.URL.Path, realtime.ManagedPathPrefix) {
			return nil // Explicitly excluded, separate managed connection owner.
		}
		return trafficrevocation.ErrUnavailable
	}
	states, err := decodeTrafficSecurity(values[0])
	if err != nil {
		return err
	}
	owner.release, err = owner.registry.AdmitAt(r.Context(), states, owner.cancel)
	if err != nil {
		return err
	}
	if cause := trafficRevocationCause(r.Context()); cause != nil {
		return cause
	}
	return r.Context().Err()
}

func refusePublicTrafficSecurity(w http.ResponseWriter, r *http.Request, err error) bool {
	if errors.Is(err, trafficrevocation.ErrUnavailable) || errors.Is(err, trafficrevocation.ErrRevoked) || errors.Is(err, trafficrevocation.ErrCapacity) {
		writeTrafficRevocationError(w, r, err)
		return true
	}
	return handleForwardRequestCancellation(w, r, true)
}
