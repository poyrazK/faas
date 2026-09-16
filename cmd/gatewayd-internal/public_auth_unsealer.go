// public_auth_unsealer bridges pkg/secretbox's identity
// loader to the narrow pkg/gateway.PublicAuthUnsealer
// interface the basic-auth branch consumes (issue #477 /
// ADR-079).
//
// Why a bridge instead of calling secretbox.OpenBytes
// directly from the gateway hot path: pkg/gateway
// deliberately has zero imports on pkg/secretbox (the same
// zero-dep posture enforceRequireAuthn has on
// pkg/auth.Middleware — the gateway package is consumed by
// every daemon that fronts the edge, including the
// in-memory fake backends in tests). The adapter is the
// single seam that:
//
//   - Splits the secretbox-namespace-prefixed plaintext
//     into username + password (the on-blob layout is
//     "<username>\n<password>\n" — newline-delimited so
//     neither field can contain the other; the apid seal
//     step writes the same shape).
//   - Validates the namespace tag (must be
//     APP_BASIC_AUTH); a tampered tag fails closed and
//     surfaces as a credential mismatch (401) so a brute-
//     forcer can't tell the difference between "no creds
//     configured" and "wrong creds".
//   - Resolves the multi-identity fallback through
//     secretbox.OpenBytesMulti on the identities slice
//     supplied at construction time (the 30-day rotation
//     overlap window — same shape the apid uses).
//
// The adapter is constructed once at boot and closed
// over the loaded identities slice; the hot path is a
// single OpenBytesMulti call + a strings.Split (cached
// for 60s by gateway.PublicAuthCache so the secretbox
// unseal doesn't run on every request).

package main

import (
	"context"
	"errors"
	"strings"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

// publicAuthUnsealer is the production implementation of
// gateway.PublicAuthUnsealer. Holds a stable snapshot of
// the loaded identities so the hot path doesn't pay a
// map-lookup per request. If the daemon's host.age is
// rotated, the operator restarts the daemon (per
// ADR-057 / the secrets rotation runbook) — the unsealer
// picks up the new identities on the next boot.
type publicAuthUnsealer struct {
	identities func() []*age.X25519Identity
}

// newPublicAuthUnsealer wraps a closure that returns the
// currently-loaded identities. nil closure → nil
// unsealer; pkg/gateway's basic-auth branch is nil-safe
// (mode='basic' returns 500 if hit) so unit tests + dev
// boxes that don't load host.age get the same behaviour
// they had pre-#477.
func newPublicAuthUnsealer(identitiesLoader func() []*age.X25519Identity) gateway.PublicAuthUnsealer {
	if identitiesLoader == nil {
		return nil
	}
	return &publicAuthUnsealer{identities: identitiesLoader}
}

// UnsealBasicAuth implements gateway.PublicAuthUnsealer.
// The flow:
//
//  1. Resolve identities via the loader closure. Empty
//     slice → error (the loader hasn't been wired; the
//     caller treats this as a credential mismatch).
//  2. Call secretbox.OpenBytesMulti, which decrypts
//     under any of the supplied identities (rotation
//     overlap) and validates the namespace tag.
//  3. Verify the returned namespace is APP_BASIC_AUTH.
//     A mismatch fails closed (defense against a future
//     seal-side namespace drift).
//  4. Split the plaintext on the first newline; both
//     halves must be non-empty.
//
// Errors are surfaced as a single opaque "unseal failed"
// error — the gateway basic-auth branch treats every
// error here as a credential mismatch so a brute-forcer
// can't distinguish between "blob tampered", "no
// identity matches", "namespace tag wrong", and
// "plaintext malformed".
func (u *publicAuthUnsealer) UnsealBasicAuth(ctx context.Context, sealed []byte) (username, password string, err error) {
	if u == nil || u.identities == nil {
		return "", "", errPublicAuthUnsealerUnavailable
	}
	identities := u.identities()
	if len(identities) == 0 {
		return "", "", errPublicAuthUnsealerUnavailable
	}
	namespace, plaintext, err := secretbox.OpenBytesMulti(identities, sealed)
	if err != nil {
		return "", "", err
	}
	if namespace != api.AppPublicAuthBasicSealNamespace {
		return "", "", errPublicAuthNamespaceMismatch
	}
	// The on-blob layout is "<username>\n<password>\n" —
	// newline-delimited so neither field can contain the
	// other. strings.Cut on the first newline is the
	// canonical primitive; the trailing newline is
	// tolerated but not required.
	user, pass, found := strings.Cut(string(plaintext), "\n")
	if !found || user == "" || pass == "" {
		return "", "", errPublicAuthPlaintextMalformed
	}
	return user, pass, nil
}

// Compile-time check: publicAuthUnsealer satisfies the
// narrow gateway.PublicAuthUnsealer interface. A drift
// in either side's signatures fails to compile here,
// surfacing before tests.
var _ gateway.PublicAuthUnsealer = (*publicAuthUnsealer)(nil)

// Sentinels for the unseal failure paths. Returned by
// value so the gateway basic-auth branch can
// errors.Is-check if a future contributor wants to
// distinguish "blob tampered" from "no creds configured"
// — for v1, every error collapses to a 401 to avoid
// leaking the distinction to a brute-forcer.
var (
	errPublicAuthUnsealerUnavailable = errors.New("public_auth_unsealer: no identities loaded")
	errPublicAuthNamespaceMismatch   = errors.New("public_auth_unsealer: namespace tag mismatch")
	errPublicAuthPlaintextMalformed  = errors.New("public_auth_unsealer: plaintext malformed")
)
