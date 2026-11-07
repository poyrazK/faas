package ingressroute

// adr: 20261104221012 — raw TCP/UDP sessions pass the account, safety and
// access gates an HTTP request passes at the gateway.

import (
	"context"
	"errors"
	"fmt"
	"net/netip"

	"github.com/onebox-faas/faas/pkg/state"
)

// ErrRefused is a raw session the app's account, safety or access settings
// refuse. Callers reject the session; nothing is woken.
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

// SafetySource is the state CheckApp reads: the app's resource account (an
// organization's status and abuse hold apply to its apps) and the live
// deployments whose security posture it checks.
type SafetySource interface {
	state.AppOwnerReader
	DeploymentSource
}

// CheckApp refuses a raw session that the HTTP gateway would refuse for the
// same app: its resource account is suspended, pending deletion or under an
// abuse hold; a live deployment is security quarantined; the app is
// internal_only; or the app has an IP allowlist that does not name the
// session's client (an unknown client address is refused). Basic and bearer
// auth cannot be checked on a raw stream and do not apply. A store without
// the reads fails closed.
func CheckApp(ctx context.Context, store any, app state.App) error {
	source, ok := store.(SafetySource)
	if !ok {
		return fmt.Errorf("%w: account and safety state are unavailable", ErrRefused)
	}
	acct, err := state.AppResourceAccount(ctx, source, app)
	if err != nil {
		return fmt.Errorf("read raw ingress account: %w", err)
	}
	if acct.Status == state.AccountSuspended || acct.Status == state.AccountDeletedPending {
		return fmt.Errorf("%w: account suspended", ErrRefused)
	}
	if acct.AbuseHeld() {
		return fmt.Errorf("%w: account under abuse hold", ErrRefused)
	}
	rows, err := source.LiveDeployments(ctx, app.ID)
	if err != nil {
		return fmt.Errorf("read raw ingress deployments: %w", err)
	}
	for _, row := range rows {
		if row.AppID == app.ID && row.ParkedReason == string(state.ParkReasonSecurityScanRegressed) {
			return fmt.Errorf("%w: app security quarantined", ErrRefused)
		}
	}
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
