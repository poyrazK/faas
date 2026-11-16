package gateway

// ADR-20261110210000 slice 3h: verify-only basic auth. Any node's edge
// checks basic auth for any app, so the edge never opens the credential: it
// looks up the argon2id verifier apid stored for the app's sealed blob
// (keyed by its SHA-256) and checks the presented credential against it.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/basicverifier"
	"github.com/onebox-faas/faas/pkg/state"
)

// BasicAuthVerifierReader reads a sealed blob's verifier
// (state.BasicAuthVerifierStore's read half).
type BasicAuthVerifierReader interface {
	AppBasicAuthVerifier(ctx context.Context, sealedSHA256 []byte) (string, error)
}

const (
	// basicAuthVerifierTTL keeps a looked-up verifier; a verifier never
	// changes for its key, so this only bounds memory and a deleted row.
	basicAuthVerifierTTL = 10 * time.Minute
	// basicAuthPassTTL keeps a credential that matched, so a client sending
	// it on every request pays one argon2id per node per minute.
	basicAuthPassTTL = time.Minute
	// basicAuthCacheMax bounds each cache; past it the cache starts over.
	basicAuthCacheMax = 10000
	// basicAuthVerifyTimeout bounds the wait for a derivation slot.
	basicAuthVerifyTimeout = 2 * time.Second
)

type expiringString struct {
	value string
	until time.Time
}

// BasicAuthVerifiers checks presented credentials against verifiers.
type BasicAuthVerifiers struct {
	store BasicAuthVerifierReader
	// fallback lets a blob with no verifier be checked by opening it; off,
	// such a blob refuses every credential.
	fallback bool
	missing  prometheus.Counter
	now      func() time.Time

	mu        sync.Mutex
	verifiers map[string]expiringString // hex key -> verifier
	passed    map[string]time.Time      // credential digest -> until
}

// NewBasicAuthVerifiers returns a checker over store. fallback keeps the
// sealed path for blobs without a verifier (until the backfill has run);
// reg, when set, receives gateway_basic_auth_verifier_missing_total.
func NewBasicAuthVerifiers(store BasicAuthVerifierReader, fallback bool, reg prometheus.Registerer) *BasicAuthVerifiers {
	v := &BasicAuthVerifiers{store: store, fallback: fallback, now: time.Now,
		verifiers: map[string]expiringString{}, passed: map[string]time.Time{},
		missing: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "gateway_basic_auth_verifier_missing_total",
			Help: "Basic-auth checks of a sealed credential with no verifier (ADR-20261110210000 slice 3h): opened instead when the sealed fallback is on, refused when it is off.",
		})}
	if reg != nil {
		_ = reg.Register(v.missing)
	}
	return v
}

// errNoVerifier: the blob has no verifier.
var errNoVerifier = errors.New("gateway: no basic auth verifier for this credential")

// check reports whether user and pass match the verifier of sealed. It
// returns errNoVerifier when the blob has none.
func (v *BasicAuthVerifiers) check(ctx context.Context, sealed []byte, user, pass string) (bool, error) {
	key := basicverifier.Key(sealed)
	hexKey := hex.EncodeToString(key)
	passKey := basicAuthPassKey(key, user, pass)
	now := v.now()
	v.mu.Lock()
	if until, ok := v.passed[passKey]; ok && now.Before(until) {
		v.mu.Unlock()
		return true, nil
	}
	cached, ok := v.verifiers[hexKey]
	v.mu.Unlock()
	verifier := cached.value
	if !ok || !now.Before(cached.until) {
		got, err := v.store.AppBasicAuthVerifier(ctx, key)
		if errors.Is(err, state.ErrNotFound) {
			v.missing.Inc()
			return false, errNoVerifier
		}
		if err != nil {
			return false, err
		}
		verifier = got
		v.mu.Lock()
		if len(v.verifiers) >= basicAuthCacheMax {
			v.verifiers = map[string]expiringString{}
		}
		v.verifiers[hexKey] = expiringString{value: verifier, until: now.Add(basicAuthVerifierTTL)}
		v.mu.Unlock()
	}
	ctx, cancel := context.WithTimeout(ctx, basicAuthVerifyTimeout)
	defer cancel()
	matched, err := basicverifier.Verify(ctx, verifier, user, pass)
	if err != nil || !matched {
		return false, err
	}
	v.mu.Lock()
	if len(v.passed) >= basicAuthCacheMax {
		v.passed = map[string]time.Time{}
	}
	v.passed[passKey] = now.Add(basicAuthPassTTL)
	v.mu.Unlock()
	return true, nil
}

// basicAuthPassKey names a presented credential for one blob without
// keeping it: a digest of the blob key, user and pass.
func basicAuthPassKey(key []byte, user, pass string) string {
	h := sha256.New()
	h.Write(key)
	h.Write([]byte{0})
	h.Write([]byte(user))
	h.Write([]byte{0})
	h.Write([]byte(pass))
	return hex.EncodeToString(h.Sum(nil))
}

// WithBasicAuthVerifiers checks basic auth against verifiers before (or,
// without the sealed fallback, instead of) opening the sealed credential.
func (h *Handler) WithBasicAuthVerifiers(v *BasicAuthVerifiers) *Handler {
	h.basicAuthVerifiers = v
	return h
}

// basicAuthMatches reports whether user and pass are the app's basic-auth
// credential; ok is false when it could not be checked (no verifier and no
// usable sealed path), which the caller answers as a mismatch.
func (h *Handler) basicAuthMatches(r *http.Request, app App, user, pass string) (matched, ok bool) {
	if h.basicAuthVerifiers != nil {
		matched, err := h.basicAuthVerifiers.check(r.Context(), app.PublicAuth.BasicSealed, user, pass)
		switch {
		case err == nil:
			return matched, true
		case !errors.Is(err, errNoVerifier) || !h.basicAuthVerifiers.fallback:
			return false, false
		}
	}
	if h.publicAuthCache == nil || h.publicAuthUnsealer == nil {
		return false, false
	}
	//nolint:contextcheck // the cache's loader closes over the request context (r.Context())
	expectedUser, expectedPass, found := h.publicAuthCache.Get(app.ID, app.PublicAuth.BasicSealed, func() (string, string, bool) {
		u, p, err := h.publicAuthUnsealer.UnsealBasicAuth(r.Context(), app.PublicAuth.BasicSealed)
		if err != nil {
			return "", "", false
		}
		return u, p, true
	})
	if !found {
		return false, false
	}
	// Constant-time compare of fixed-length digests, both evaluated.
	return basicCredentialsMatch(user, pass, expectedUser, expectedPass), true
}
