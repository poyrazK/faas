// adr: 20261106114500 — apid issues key-scoped bindings only where the
// gateway that enforces them is released, and only literal prefixes.
package object

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type keyScopeHost struct {
	Host
	released bool
	asked    []string
}

func (h *keyScopeHost) CapabilityEnabledFor(capability string, _ state.Account, _ state.App) bool {
	h.asked = append(h.asked, capability)
	return h.released
}

type hostWithoutCapabilities struct{ Host }

func TestKeyScopedBindingsNeedTheCapability(t *testing.T) {
	acct, app := state.Account{ID: "acct"}, state.App{ID: "app"}

	held := &keyScopeHost{}
	if p := (&Service{Host: held}).keyScopeProblem(acct, app, "segments/L0/"); p == nil || p.Status != http.StatusForbidden || p.Code != "object_storage_key_scopes_unavailable" {
		t.Fatalf("unreleased = %+v, want 403 object_storage_key_scopes_unavailable", p)
	}
	if len(held.asked) != 1 || held.asked[0] != api.ObjectStorageKeyScopesCapability {
		t.Fatalf("capabilities asked = %v", held.asked)
	}
	if p := (&Service{Host: hostWithoutCapabilities{}}).keyScopeProblem(acct, app, "segments/L0/"); p == nil || p.Status != http.StatusForbidden {
		t.Fatalf("a host that cannot decide capabilities = %+v, want a refusal", p)
	}
	released := &Service{Host: &keyScopeHost{released: true}}
	if p := released.keyScopeProblem(acct, app, "segments/L0/"); p != nil {
		t.Fatalf("released = %+v, want admitted", p)
	}
	for _, prefix := range []string{"v1/*/L0/", "a?b/", "x[1]/", "../up/", "a//b/", "/abs/", "tenant1", "a\x01/", strings.Repeat("a", api.MaxObjectKeyScopeBytes) + "/"} {
		if p := released.keyScopeProblem(acct, app, prefix); p == nil || p.Status != http.StatusBadRequest {
			t.Errorf("key_prefix %.20q = %+v, want 400", prefix, p)
		}
	}
}

type listOnlyBindingStore struct {
	state.ObjectS3CredentialBindingStore
	credentials []state.ObjectS3Credential
	listed      int
}

func (s *listOnlyBindingStore) ListObjectS3Credentials(context.Context, string, string) ([]state.ObjectS3Credential, error) {
	s.listed++
	return s.credentials, nil
}

// The server-side gregale.yaml apply honors key_prefix: the same release
// gate as the API, and no silent reuse of a binding with another prefix.
func TestManifestBucketBindingHonorsKeyPrefix(t *testing.T) {
	ctx := context.Background()
	acct, app := state.Account{ID: "acct"}, state.App{ID: "app", Slug: "store"}
	bucket := state.ObjectBucket{ID: "bkt", Name: "segments", Scope: "default"}
	store := &listOnlyBindingStore{credentials: []state.ObjectS3Credential{{
		Status: state.ObjectS3CredentialStatusActive, ManagedAppID: "app", ManagedScope: "default", ManagedPrefix: "GREGALE_S3_SEGMENTS",
		Permission: state.ObjectBucketPermissionReadWrite,
	}}}
	want := ManifestBucket{Name: "segments", Prefix: "GREGALE_S3_SEGMENTS", Permission: state.ObjectBucketPermissionReadWrite, KeyPrefix: "segments/L0/"}

	if p, err := (&Service{Host: &keyScopeHost{}}).ensureManifestBucketBinding(ctx, acct, app, bucket, store, want); err != nil || p == nil || p.Code != "object_storage_key_scopes_unavailable" || store.listed != 0 {
		t.Fatalf("unreleased scoped apply = %+v, %v (store listed %d times); want the release refusal before the store", p, err, store.listed)
	}
	released := &Service{Host: &keyScopeHost{released: true}}
	if p, err := released.ensureManifestBucketBinding(ctx, acct, app, bucket, store, want); p != nil || !errors.Is(err, state.ErrConflict) || !strings.Contains(err.Error(), "key_prefix") {
		t.Fatalf("scoped apply over a whole-bucket binding = %+v, %v; want a key_prefix conflict", p, err)
	}
	whole := want
	whole.KeyPrefix = ""
	if p, err := released.ensureManifestBucketBinding(ctx, acct, app, bucket, store, whole); p != nil || err != nil {
		t.Fatalf("unscoped apply over the same binding = %+v, %v; want it reused", p, err)
	}
}
