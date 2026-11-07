package ingressroute

// adr: 20261104221012 — raw TCP/UDP sessions pass the public access gates an
// HTTP request passes at the gateway.

import (
	"context"
	"errors"
	"fmt"
	"net/netip"

	"github.com/onebox-faas/faas/pkg/state"
)

// ErrRefused is a raw session the app's public access setting refuses. Callers reject the session; nothing is woken.
var ErrRefused = errors.New("raw ingress refused")

type peerKey struct{}

// WithPeer records the session's client address for CheckApp's IP
// allowlist.
func WithPeer(ctx context.Context, peer netip.Addr) context.Context {
	return context.WithValue(ctx, peerKey{}, peer.Unmap())
}

// PeerFrom returns the client address WithPeer recorded.
func PeerFrom(ctx context.Context) (netip.Addr, bool) {
	peer, ok := ctx.Value(peerKey{}).(netip.Addr)
	return peer, ok && peer.IsValid()
}

// CheckApp refuses a raw session the app's public access setting refuses at
// the gateway: the app is internal_only, or it has an IP allowlist that does
// not name the session's client (an unknown client address is refused).
// Basic and bearer auth cannot be checked on a raw stream and do not apply.
// The account owner (RequireActiveOwner) and security quarantine
// (RequireDeployment) are checked by the callers.
func CheckApp(ctx context.Context, app state.App) error {
	switch app.PublicAuthMode {
	case state.AppPublicAuthModeInternalOnly:
		return fmt.Errorf("%w: app is internal_only", ErrRefused)
	case state.AppPublicAuthModeIPAllowlist:
		peer, ok := PeerFrom(ctx)
		if !ok || !allowlisted(app.PublicAuthIPAllowlist, peer) {
			return fmt.Errorf("%w: client address not in the app's IP allowlist", ErrRefused)
		}
	}
	return nil
}

func allowlisted(prefixes []netip.Prefix, peer netip.Addr) bool {
	for _, p := range prefixes {
		if p.Contains(peer) {
			return true
		}
	}
	return false
}
