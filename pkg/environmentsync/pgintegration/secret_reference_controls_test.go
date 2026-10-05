package pgintegration_test

import (
	"bytes"
	"errors"
	"maps"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestEnvironmentSecretReferenceControlsNamesSourcesAndIdentity(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		store, source, _, app := secretRefFixture(t, basic, "report")
		controls := basic.(state.AppEnvironmentSecretReferenceControlStore)
		snapshot, err := controls.ReadAppEnvironmentSecretReferences(t.Context(), source.AccountID, app.ID, "staging")
		if err != nil || snapshot.Target.EnvironmentID == source.EnvironmentID || snapshot.Count != 2 || !maps.Equal(snapshot.References, map[string]string{"DATABASE_URL": "secret:DATABASE_A"}) {
			t.Fatalf("names/catalog/quota snapshot: %+v %v", snapshot, err)
		}
		if err := store.UpsertAppSecretInScope(t.Context(), source.AccountID, app.ID, "production", "PRODUCTION_ONLY", []byte("sealed-production-only")); err != nil {
			t.Fatal(err)
		}
		if err := controls.SetAppEnvironmentSecretReference(t.Context(), snapshot.Target, "URL", "secret:PRODUCTION_ONLY"); !errors.Is(err, state.ErrEnvironmentSecretReferenceSourceNotFound) {
			t.Fatalf("source fell back across environments: %v", err)
		}
		if err := controls.SetAppEnvironmentSecretReference(t.Context(), snapshot.Target, "DATABASE_URL", "secret:DATABASE_B"); err != nil {
			t.Fatal(err)
		}
		sealed, err := store.GetAppSecretInScope(t.Context(), source.AccountID, app.ID, "staging", "DATABASE_B")
		if err != nil || !bytes.Equal(sealed.Ciphertext, []byte("staging-sealed-DATABASE_B")) {
			t.Fatalf("changing names modified source: %+v %v", sealed, err)
		}
		if err := store.UpsertAppEnvInScope(t.Context(), source.AccountID, app.ID, "staging", "PLAIN", "plain"); err != nil {
			t.Fatal(err)
		}
		if err := controls.SetAppEnvironmentSecretReference(t.Context(), snapshot.Target, "PLAIN", "secret:DATABASE_B"); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("plaintext shadow accepted: %v", err)
		}
		if err := store.DeleteProjectEnvironment(t.Context(), source.AccountID, source.ProjectID, "staging"); err != nil {
			t.Fatal(err)
		}
		replacement, err := store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: source.AccountID, ProjectID: source.ProjectID, Slug: "staging"})
		if err != nil || replacement.ID == snapshot.Target.EnvironmentID {
			t.Fatalf("replacement: %+v %v", replacement, err)
		}
		for _, mutation := range []func() error{
			func() error {
				return controls.SetAppEnvironmentSecretReference(t.Context(), snapshot.Target, "DATABASE_URL", "secret:DATABASE_B")
			},
			func() error {
				return controls.RemoveAppEnvironmentSecretReference(t.Context(), snapshot.Target, "DATABASE_URL")
			},
		} {
			if err := mutation(); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("old catalog target mutated replacement: %v", err)
			}
		}
		if _, err := controls.ReadAppEnvironmentSecretReferences(t.Context(), "00000000-0000-0000-0000-000000000000", app.ID, "production"); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("cross-tenant snapshot: %v", err)
		}
		if _, err := controls.ReadAppEnvironmentSecretReferences(t.Context(), source.AccountID, app.ID, "missing"); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("unregistered snapshot: %v", err)
		}
	})
}

func TestEnvironmentSecretReferenceControlsHonorAuthorityAndOverrides(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		store, source, _, app := secretRefFixture(t, basic, "enforce")
		adoptSecretRefs(t, store, source)
		controls := basic.(state.AppEnvironmentSecretReferenceControlStore)
		snapshot, err := controls.ReadAppEnvironmentSecretReferences(t.Context(), source.AccountID, app.ID, "production")
		if err != nil {
			t.Fatal(err)
		}
		if err := controls.SetAppEnvironmentSecretReference(t.Context(), snapshot.Target, "DATABASE_URL", "secret:ABSENT"); !errors.Is(err, state.ErrEnvironmentGitManaged) {
			t.Fatalf("source validation hid ownership: %v", err)
		}
		if err := controls.RemoveAppEnvironmentSecretReference(t.Context(), snapshot.Target, "DATABASE_URL"); !errors.Is(err, state.ErrEnvironmentGitManaged) {
			t.Fatalf("owned delete: %v", err)
		}
		override := state.EnvironmentGitOpsOverrideRequest{Resource: "workload/api", Path: "secret_refs/DATABASE_URL", Reason: "database incident", ExpiresAt: time.Now().UTC().Add(time.Hour)}
		if err := store.SetEnvironmentGitOpsOverride(t.Context(), source.AccountID, source.ID, override); err != nil {
			t.Fatal(err)
		}
		if err := controls.RemoveAppEnvironmentSecretReference(t.Context(), snapshot.Target, "DATABASE_URL"); err != nil {
			t.Fatal(err)
		}
		if err := store.RemoveEnvironmentGitOpsOverride(t.Context(), source.AccountID, source.ID, override.Resource, override.Path); err != nil {
			t.Fatal(err)
		}
		// Ownership must protect even an absent row after the override expires.
		if err := controls.RemoveAppEnvironmentSecretReference(t.Context(), snapshot.Target, "DATABASE_URL"); !errors.Is(err, state.ErrEnvironmentGitManaged) {
			t.Fatalf("absent owned delete bypassed authority: %v", err)
		}
	})
}
