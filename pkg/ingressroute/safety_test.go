package ingressroute

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

type safetyFixture struct {
	acct state.Account
	deps []state.Deployment
}

func (s safetyFixture) AccountByID(context.Context, string) (state.Account, error) {
	return s.acct, nil
}
func (s safetyFixture) OrgByID(context.Context, string) (state.Org, error) {
	return state.Org{}, state.ErrNotFound
}
func (s safetyFixture) LiveDeployments(context.Context, string) ([]state.Deployment, error) {
	return s.deps, nil
}

// ADR-20261104221012: a raw TCP/UDP session meets the gates an HTTP request
// meets at the gateway.
func TestCheckAppRefusesWhatTheGatewayRefuses(t *testing.T) {
	held := time.Now()
	active := state.Account{ID: "acct", Status: state.AccountActive}
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
		store   any
		app     state.App
		refused bool
	}{
		{name: "active", ctx: context.Background(), store: safetyFixture{acct: active}, app: app},
		{name: "suspended", ctx: context.Background(), store: safetyFixture{acct: state.Account{ID: "acct", Status: state.AccountSuspended}}, app: app, refused: true},
		{name: "deletion pending", ctx: context.Background(), store: safetyFixture{acct: state.Account{ID: "acct", Status: state.AccountDeletedPending}}, app: app, refused: true},
		{name: "abuse hold", ctx: context.Background(), store: safetyFixture{acct: state.Account{ID: "acct", Status: state.AccountActive, AbuseHoldAt: &held}}, app: app, refused: true},
		{name: "quarantined", ctx: context.Background(), store: safetyFixture{acct: active, deps: []state.Deployment{{AppID: "app", ParkedReason: string(state.ParkReasonSecurityScanRegressed)}}}, app: app, refused: true},
		{name: "internal_only", ctx: context.Background(), store: safetyFixture{acct: active}, app: internalOnly, refused: true},
		{name: "allowlisted client", ctx: inside, store: safetyFixture{acct: active}, app: allowlisted},
		{name: "client outside allowlist", ctx: outside, store: safetyFixture{acct: active}, app: allowlisted, refused: true},
		{name: "allowlist without client address", ctx: context.Background(), store: safetyFixture{acct: active}, app: allowlisted, refused: true},
		{name: "basic auth does not apply", ctx: context.Background(), store: safetyFixture{acct: active}, app: basic},
		{name: "store without the reads", ctx: context.Background(), store: struct{}{}, app: app, refused: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckApp(tc.ctx, tc.store, tc.app)
			if tc.refused != errors.Is(err, ErrRefused) {
				t.Fatalf("CheckApp = %v, refused want %v", err, tc.refused)
			}
			if !tc.refused && err != nil {
				t.Fatalf("CheckApp = %v", err)
			}
		})
	}
}
