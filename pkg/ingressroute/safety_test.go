package ingressroute

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261104221012: a raw TCP/UDP session meets the public access gates an
// HTTP request meets at the gateway.
func TestCheckAppRefusesWhatTheGatewayRefuses(t *testing.T) {
	app := state.App{ID: "app", AccountID: "acct"}
	allowlisted := app
	allowlisted.PublicAuthMode = state.AppPublicAuthModeIPAllowlist
	allowlisted.PublicAuthIPAllowlist = []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24")}
	internalOnly := app
	internalOnly.PublicAuthMode = state.AppPublicAuthModeInternalOnly
	basic := app
	basic.PublicAuthMode = state.AppPublicAuthModeBasic
	inside := WithPeer(context.Background(), netip.MustParseAddr("198.51.100.7"))
	outside := WithPeer(context.Background(), netip.MustParseAddr("203.0.113.7"))
	for _, tc := range []struct {
		name    string
		ctx     context.Context
		app     state.App
		refused bool
	}{
		{name: "open", ctx: context.Background(), app: app},
		{name: "internal_only", ctx: context.Background(), app: internalOnly, refused: true},
		{name: "allowlisted client", ctx: inside, app: allowlisted},
		{name: "client outside allowlist", ctx: outside, app: allowlisted, refused: true},
		{name: "allowlist without client address", ctx: context.Background(), app: allowlisted, refused: true},
		{name: "basic auth does not apply", ctx: context.Background(), app: basic},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckApp(tc.ctx, tc.app)
			if tc.refused != errors.Is(err, ErrRefused) {
				t.Fatalf("CheckApp = %v, refused want %v", err, tc.refused)
			}
			if !tc.refused && err != nil {
				t.Fatalf("CheckApp = %v", err)
			}
		})
	}
}
