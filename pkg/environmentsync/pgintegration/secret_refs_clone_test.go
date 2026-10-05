package pgintegration_test

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type secretRefCloneStore interface {
	secretRefTestStore
	CloneProjectEnvironment(context.Context, state.ProjectEnvironmentClone, api.Limits) (state.ProjectEnvironment, state.ProjectEnvironmentCloneResult, error)
	RollbackProjectEnvironmentClone(context.Context, string, string, string) error
}

func TestEnvironmentGitOpsSecretReferenceConcurrentClonesShareQuota(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		refs, source, _, app := secretRefFixture(t, basic, "enforce")
		store := refs.(secretRefCloneStore)
		limits := api.MustLimitsFor(api.PlanPro)
		limits.EnvVarsMax = 3 // Two existing references leave room for one clone.
		start := make(chan struct{})
		results := make(chan error, 2)
		var workers sync.WaitGroup
		for _, target := range []string{"preview-one", "preview-two"} {
			workers.Add(1)
			go func() {
				defer workers.Done()
				<-start
				_, _, err := store.CloneProjectEnvironment(t.Context(), state.ProjectEnvironmentClone{
					AccountID: source.AccountID, ProjectID: source.ProjectID, SourceSlug: "production", TargetSlug: target,
				}, limits)
				results <- err
			}()
		}
		close(start)
		workers.Wait()
		close(results)
		accepted, rejected := 0, 0
		for err := range results {
			if err == nil {
				accepted++
			} else if errors.Is(err, state.ErrProjectEnvironmentCloneQuota) {
				rejected++
			} else {
				t.Fatalf("concurrent clone: %v", err)
			}
		}
		if accepted != 1 || rejected != 1 {
			t.Fatalf("shared quota: accepted=%d rejected=%d", accepted, rejected)
		}
		if count, err := store.CountAppEnv(t.Context(), source.AccountID, app.ID); err != nil || count != 3 {
			t.Fatalf("concurrent clone spent quota twice: %d %v", count, err)
		}
		catalogs := 0
		for _, target := range []string{"preview-one", "preview-two"} {
			if _, err := store.ProjectEnvironmentBySlug(t.Context(), source.AccountID, source.ProjectID, target); err == nil {
				catalogs++
			} else if !errors.Is(err, state.ErrNotFound) {
				t.Fatal(err)
			}
		}
		if catalogs != 1 {
			t.Fatalf("failed concurrent clone retained catalog: %d", catalogs)
		}
	})
}

func TestEnvironmentGitOpsSecretReferenceClonePreservesIntentWithoutOwnership(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		refs, source, _, app := secretRefFixture(t, basic, "enforce")
		store := refs.(secretRefCloneStore)
		adoptSecretRefs(t, refs, source)
		created, result, err := store.CloneProjectEnvironment(t.Context(), state.ProjectEnvironmentClone{
			AccountID: source.AccountID, ProjectID: source.ProjectID, SourceSlug: "production", TargetSlug: "preview",
		}, api.MustLimitsFor(api.PlanPro))
		if err != nil || created.ID == source.EnvironmentID || result.SecretReferencesCopied != 1 || result.SecretsCopied != 2 || result.VariablesCopied != 0 {
			t.Fatalf("clone: %+v %+v %v", created, result, err)
		}
		// Copy the current adopted value, even though the approved definition
		// selects DATABASE_B. A clone does not apply the source's pending drift.
		assertSecretRef(t, refs, source, app, "preview", "DATABASE_URL", "secret:DATABASE_A")
		assertSecretRef(t, refs, source, app, "staging", "DATABASE_URL", "secret:DATABASE_A")
		if _, err := store.EnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, "preview"); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("clone inherited Git manager: %v", err)
		}
		if err := store.PutAppEnvironmentSecretReference(t.Context(), source.AccountID, app.ID, "preview", "DATABASE_URL", "secret:DATABASE_B"); err != nil {
			t.Fatalf("clone inherited source ownership: %v", err)
		}
		if err := store.PutAppEnvironmentSecretReference(t.Context(), source.AccountID, app.ID, "production", "DATABASE_URL", "secret:DATABASE_B"); !errors.Is(err, state.ErrEnvironmentGitManaged) {
			t.Fatalf("source ownership changed: %v", err)
		}
		assertSecretRef(t, refs, source, app, "production", "DATABASE_URL", "secret:DATABASE_A")
		original, err := store.GetAppSecretInScope(t.Context(), source.AccountID, app.ID, "production", "DATABASE_A")
		if err != nil {
			t.Fatal(err)
		}
		copied, err := store.GetAppSecretInScope(t.Context(), source.AccountID, app.ID, "preview", "DATABASE_A")
		if err != nil || !bytes.Equal(original.Ciphertext, copied.Ciphertext) || copied.DeliveredVersion != 0 || copied.DeliveryStatus != state.SecretDeliveryPending {
			t.Fatalf("cloned secret delivery metadata: %+v %v", copied, err)
		}
		if count, err := store.CountAppEnv(t.Context(), source.AccountID, app.ID); err != nil || count != 3 {
			t.Fatalf("shared reference quota count: %d %v", count, err)
		}
	})
}

func TestEnvironmentGitOpsSecretReferenceCloneQuotaIsAtomic(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		refs, source, _, app := secretRefFixture(t, basic, "enforce")
		store := refs.(secretRefCloneStore)
		if err := store.UpsertAppEnvInScope(t.Context(), source.AccountID, app.ID, "production", "MODE", "production"); err != nil {
			t.Fatal(err)
		}
		clone := state.ProjectEnvironmentClone{AccountID: source.AccountID, ProjectID: source.ProjectID, SourceSlug: "production", TargetSlug: "preview"}
		limits := api.MustLimitsFor(api.PlanPro)
		limits.EnvVarsMax = 4 // Three existing keys plus two copied keys exceed four.
		_, _, err := store.CloneProjectEnvironment(t.Context(), clone, limits)
		var quota *state.ProjectEnvironmentCloneQuotaError
		if !errors.As(err, &quota) || quota.Resource != "variables" || quota.WorkloadSlug != app.Slug || quota.Observed != 5 {
			t.Fatalf("clone quota: %+v %v", quota, err)
		}
		if _, err := store.ProjectEnvironmentBySlug(t.Context(), source.AccountID, source.ProjectID, "preview"); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("failed clone left catalog identity: %v", err)
		}
		if rows, err := store.ListAppSecretsInScope(t.Context(), source.AccountID, app.ID, "preview"); err != nil || len(rows) != 0 {
			t.Fatalf("failed clone left sealed sources: %d %v", len(rows), err)
		}
		if count, err := store.CountAppEnvInScope(t.Context(), source.AccountID, app.ID, "preview"); err != nil || count != 0 {
			t.Fatalf("failed clone left variable/reference intent: %d %v", count, err)
		}
		limits.EnvVarsMax = 5
		_, result, err := store.CloneProjectEnvironment(t.Context(), clone, limits)
		if err != nil || result.VariablesCopied != 1 || result.SecretReferencesCopied != 1 {
			t.Fatalf("exact quota clone: %+v %v", result, err)
		}
		if count, err := store.CountAppEnv(t.Context(), source.AccountID, app.ID); err != nil || count != 5 {
			t.Fatalf("exact quota total: %d %v", count, err)
		}
	})
}

func TestEnvironmentGitOpsSecretReferenceCloneRollbackDropsCatalogReferences(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		refs, source, _, app := secretRefFixture(t, basic, "enforce")
		store := refs.(secretRefCloneStore)
		created, _, err := store.CloneProjectEnvironment(t.Context(), state.ProjectEnvironmentClone{
			AccountID: source.AccountID, ProjectID: source.ProjectID, SourceSlug: "production", TargetSlug: "preview",
		}, api.MustLimitsFor(api.PlanPro))
		if err != nil {
			t.Fatal(err)
		}
		if err := store.RollbackProjectEnvironmentClone(t.Context(), source.AccountID, source.ProjectID, "preview"); err != nil {
			t.Fatal(err)
		}
		replacement, err := store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: source.AccountID, ProjectID: source.ProjectID, Slug: "preview"})
		if err != nil || replacement.ID == created.ID {
			t.Fatalf("replacement catalog identity: %+v %v", replacement, err)
		}
		if got, err := store.AppEnvironmentSecretReferences(t.Context(), source.AccountID, app.ID, "preview"); err != nil || len(got) != 0 {
			t.Fatalf("replacement inherited failed clone references: %+v %v", got, err)
		}
		if count, err := store.CountAppEnv(t.Context(), source.AccountID, app.ID); err != nil || count != 2 {
			t.Fatalf("failed clone retained quota: %d %v", count, err)
		}
		assertSecretRef(t, refs, source, app, "production", "DATABASE_URL", "secret:DATABASE_A")
	})
}
