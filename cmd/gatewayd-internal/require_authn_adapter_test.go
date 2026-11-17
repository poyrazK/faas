// Adapter unit tests for the per-deployment authz chain
// (issue #560). Pins the state.Err* → gateway.Err* sentinel
// translation that production relies on, plus the success
// path's state.Account / state.APIKey → gateway.RequireAuthnAccount
// / gateway.RequireAuthnKey ID-only conversion.
//
// End-to-end behaviour (missing / invalid / expired / cross-
// account) lives in pkg/gateway/require_authn_test.go; this
// file pins the bridge's contract so a drift in either
// sentinel's string value or in the conversion is caught
// at the unit boundary.
package main

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// stubAuthn is the narrow Authenticator surface the adapter
// reaches into. The real pkg/auth.Middleware.Authn is
// unreachable from a unit test (it carries a session manager
// + audit seam that need wiring), so we stand up a minimal
// in-package fake that returns whatever each test seeds.
// AccountByID / AppBySlug / TouchKeyLastUsed return
// state.ErrNotFound unconditionally — the adapter never
// calls them, so the values are placeholders that keep
// the interface satisfied.
type stubAuthn struct {
	acct state.Account
	key  state.APIKey
	err  error
}

func (s *stubAuthn) AuthenticateKey(_ context.Context, _ []byte) (state.Account, state.APIKey, error) {
	return s.acct, s.key, s.err
}

func (s *stubAuthn) AccountByID(_ context.Context, _ string) (state.Account, error) {
	return state.Account{}, state.ErrNotFound
}

func (s *stubAuthn) AppBySlug(_ context.Context, _ string) (state.App, error) {
	return state.App{}, state.ErrNotFound
}

func (s *stubAuthn) TouchKeyLastUsed(_ context.Context, _ string) error {
	return nil
}

// AuthenticateOIDCBearer is the ADR-101 / PR-A stub for the
// new interface method. The require_authn_adapter test only
// exercises the Authn field dispatches — it does not call
// AuthenticateOIDCBearer — so a nil-error no-op is enough to
// satisfy the interface. Production gatewayd-internal routes
// (added in PR-B) will exercise this through the standard
// bearer middleware path.
func (s *stubAuthn) AuthenticateOIDCBearer(_ context.Context, _ []byte) (state.Account, state.APIKey, error) {
	return state.Account{}, state.APIKey{}, nil
}

// stubOnlyAuthnField builds an *authmw.Middleware whose
// exported Authn field points at the supplied stub. The
// other Middleware fields (Sessions / Lookups / Audit /
// Limiter) are left zero; the adapter only reads Authn.
// Returning a partially-constructed *authmw.Middleware is
// safe here because the adapter's only use of the receiver
// is the .Authn field access.
func stubOnlyAuthnField(s *stubAuthn) *middleware.Middleware {
	return &middleware.Middleware{Authn: s}
}

// TestRequireAuthnAdapter_SuccessCarriesIDs — success path:
// the adapter converts state.Account.ID / state.APIKey.ID
// into the narrow RequireAuthnAccount{ID} / RequireAuthnKey{ID}
// pair the gateway expects, with err=nil so the handler's
// cross-account branch fires next.
func TestRequireAuthnAdapter_SuccessCarriesIDs(t *testing.T) {
	stub := &stubAuthn{
		acct: state.Account{ID: "acct-1"},
		key:  state.APIKey{ID: "key-1"},
	}
	adapter := newRequireAuthnAdapter(stubOnlyAuthnField(stub), nil)

	gwAcct, gwKey, err := adapter.AuthenticateKey(context.Background(), api.HashAPIKey("fp_live_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"))
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if gwAcct.ID != "acct-1" {
		t.Errorf("acct.ID = %q, want acct-1", gwAcct.ID)
	}
	if gwKey.ID != "key-1" {
		t.Errorf("key.ID = %q, want key-1", gwKey.ID)
	}
}

// TestRequireAuthnAdapter_TranslatesExpired — the
// state.ErrAPIKeyExpired sentinel must surface as the
// gateway-local gateway.ErrAPIKeyExpired. errors.Is on the
// returned error must match the gateway sentinel; a drift
// in either string value breaks the handler's "reason =
// expired" audit classification.
func TestRequireAuthnAdapter_TranslatesExpired(t *testing.T) {
	stub := &stubAuthn{err: state.ErrAPIKeyExpired}
	adapter := newRequireAuthnAdapter(stubOnlyAuthnField(stub), nil)

	_, _, err := adapter.AuthenticateKey(context.Background(), api.HashAPIKey("fp_live_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"))
	if !errors.Is(err, gateway.ErrAPIKeyExpired) {
		t.Fatalf("err = %v, want errors.Is(., gateway.ErrAPIKeyExpired) true", err)
	}
}

// TestRequireAuthnAdapter_TranslatesRevoked — same shape
// as the expired test, for the revoked sentinel.
func TestRequireAuthnAdapter_TranslatesRevoked(t *testing.T) {
	stub := &stubAuthn{err: state.ErrAPIKeyRevoked}
	adapter := newRequireAuthnAdapter(stubOnlyAuthnField(stub), nil)

	_, _, err := adapter.AuthenticateKey(context.Background(), api.HashAPIKey("fp_live_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"))
	if !errors.Is(err, gateway.ErrAPIKeyRevoked) {
		t.Fatalf("err = %v, want errors.Is(., gateway.ErrAPIKeyRevoked) true", err)
	}
}

// TestRequireAuthnAdapter_UnknownKeyPassesThrough — a
// non-sentinel error (e.g. "no row matches this hash") must
// NOT be translated into either gateway sentinel. The
// handler's audit classification falls through to
// reason="invalid_bearer" only when errors.Is returns
// false for both sentinels; passing through a wrapped
// state.ErrAPIKeyExpired would silently mis-classify.
func TestRequireAuthnAdapter_UnknownKeyPassesThrough(t *testing.T) {
	stub := &stubAuthn{err: errors.New("no api_keys row matches this hash")}
	adapter := newRequireAuthnAdapter(stubOnlyAuthnField(stub), nil)

	_, _, err := adapter.AuthenticateKey(context.Background(), api.HashAPIKey("fp_live_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"))
	if errors.Is(err, gateway.ErrAPIKeyExpired) {
		t.Errorf("non-sentinel err incorrectly matched gateway.ErrAPIKeyExpired: %v", err)
	}
	if errors.Is(err, gateway.ErrAPIKeyRevoked) {
		t.Errorf("non-sentinel err incorrectly matched gateway.ErrAPIKeyRevoked: %v", err)
	}
	if err == nil {
		t.Error("err = nil, want pass-through of the stub's error")
	}
}

// TestRequireAuthnAdapter_NilMiddlewareReturnsNil —
// production nil-safety: a nil middleware arg → nil
// adapter. The pkg/gateway authz branch is nil-safe on the
// adapter field; this pins that the constructor honours
// the nil-in-nil-out contract (a future change that
// returned a half-built adapter would crash on every
// gated-app request).
func TestRequireAuthnAdapter_NilMiddlewareReturnsNil(t *testing.T) {
	if got := newRequireAuthnAdapter(nil, nil); got != nil {
		t.Errorf("newRequireAuthnAdapter(nil, nil) = %v, want nil", got)
	}
}
