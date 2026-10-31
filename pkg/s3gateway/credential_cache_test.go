// adr: 20261030075301

package s3gateway

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type credentialClock struct{ now time.Time }

func (c *credentialClock) Now() time.Time      { return c.now }
func (c *credentialClock) Add(d time.Duration) { c.now = c.now.Add(d) }

// TestCredentialCacheServesReadsOnly: GET and HEAD reuse a resolution, with
// its opened secret, for 2 s; writes resolve every time, so a revoked
// credential's writes stop at once (ADR-20261030075301 step 4).
func TestCredentialCacheServesReadsOnly(t *testing.T) {
	h, store, _ := newGatewayTestHandler(t, state.ObjectBucketPermissionReadWrite, nil)
	clock := &credentialClock{now: time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)}
	h.credentials = NewCredentialCache(clock.Now)
	ctx := t.Context()
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodGet} {
		r, err := h.resolveCredential(ctx, method, testAccess)
		if err != nil || r.secret != testSecret || r.credential.ID != "credential" {
			t.Fatalf("%s: %+v %v", method, r, err)
		}
	}
	if store.resolves != 1 {
		t.Fatalf("three reads within the TTL resolved %d times", store.resolves)
	}
	for range 2 {
		if _, err := h.resolveCredential(ctx, http.MethodPut, testAccess); err != nil {
			t.Fatal(err)
		}
	}
	if store.resolves != 3 {
		t.Fatalf("writes reused a cached resolution: %d resolutions", store.resolves)
	}

	store.credential.Status = state.ObjectS3CredentialStatusRevoked
	store.resolveErr = state.ErrNotFound
	if _, err := h.resolveCredential(ctx, http.MethodGet, testAccess); err != nil {
		t.Fatal("a revocation reached reads before the TTL", err)
	}
	clock.Add(api.ObjectReadAdmissionTTL)
	if _, err := h.resolveCredential(ctx, http.MethodGet, testAccess); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("a revoked credential still read after the TTL: %v", err)
	}
	store.resolveErr = errors.New("database unreachable")
	if _, err := h.resolveCredential(ctx, http.MethodGet, testAccess); err == nil {
		t.Fatal("a revoked credential's stale resolution was served during an outage")
	}
}

func TestCredentialCacheOutages(t *testing.T) {
	h, store, _ := newGatewayTestHandler(t, state.ObjectBucketPermissionRead, nil)
	clock := &credentialClock{now: time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)}
	h.credentials = NewCredentialCache(clock.Now)
	ctx := t.Context()
	if _, err := h.resolveCredential(ctx, http.MethodGet, testAccess); err != nil {
		t.Fatal(err)
	}
	store.resolveErr = errors.New("database unreachable")
	clock.Add(api.ObjectReadAdmissionMaxStale - time.Second)
	if _, err := h.resolveCredential(ctx, http.MethodGet, testAccess); err != nil {
		t.Fatal("an outage within the stale window refused a read", err)
	}
	clock.Add(time.Second)
	if _, err := h.resolveCredential(ctx, http.MethodGet, testAccess); err == nil {
		t.Fatal("an outage past the stale window still served the resolution")
	}
}

func TestCredentialCacheSkipsSignedURLCredentials(t *testing.T) {
	h, store, _ := newGatewayTestHandler(t, state.ObjectBucketPermissionRead, nil)
	store.credential.URL = &state.ObjectURLCapability{ExpiresAt: time.Now().Add(time.Minute)}
	h.credentials = NewCredentialCache(nil)
	for range 2 {
		if _, err := h.resolveCredential(t.Context(), http.MethodGet, testAccess); err != nil {
			t.Fatal(err)
		}
	}
	if store.resolves != 2 {
		t.Fatalf("a signed-URL credential was cached: %d resolutions", store.resolves)
	}
}
