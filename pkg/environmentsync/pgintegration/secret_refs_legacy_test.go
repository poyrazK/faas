package pgintegration_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

func legacySecretRefFixture(t *testing.T, basic gitOpsTestStore, refs json.RawMessage, destination string) (secretRefTestStore, state.EnvironmentGitSource, environmentsync.DesiredState, state.App, state.Deployment) {
	t.Helper()
	store := basic.(secretRefTestStore)
	source, desired := seedMode(t, basic, "enforce")
	app, err := store.CreateApp(t.Context(), state.App{AccountID: source.AccountID, ProjectID: source.ProjectID, Slug: "shop-api", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 1, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"DATABASE_A", "DATABASE_B"} {
		if err := store.UpsertAppSecretInScope(t.Context(), source.AccountID, app.ID, "production", name, []byte("sealed-"+name)); err != nil {
			t.Fatal(err)
		}
	}
	dep, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, Status: state.DeployLive, ImageDigest: "sha256:abc", OverrideEnvSecrets: refs})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateDeploymentStatus(t.Context(), dep.ID, state.DeployLive, ""); err != nil {
		t.Fatal(err)
	}
	dep.Status = state.DeployLive
	desired.Definition.Workloads["api"] = api.EnvironmentWorkload{App: app.Slug, SecretRefs: map[string]string{destination: "secret:DATABASE_B"}}
	desired, err = environmentsync.Compile(desired.Definition)
	if err != nil {
		t.Fatal(err)
	}
	source, _, err = store.ApproveEnvironmentDesiredRevision(t.Context(), approval(source, desired, strings.Repeat("a", 40)))
	if err != nil {
		t.Fatal(err)
	}
	return store, source, desired, app, dep
}

func TestEnvironmentGitOpsLegacySecretReferenceAdoptionPreservesThenReconciles(t *testing.T) {
	for _, tc := range []struct {
		name, key string
		refs      json.RawMessage
	}{
		{"explicit", "DATABASE_URL", json.RawMessage(`{"DATABASE_URL":"secret:DATABASE_A"}`)},
		{"automatic", "DATABASE_A", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stores(t, func(t *testing.T, basic gitOpsTestStore) {
				store, source, desired, app, _ := legacySecretRefFixture(t, basic, tc.refs, tc.key)
				preview, err := store.PreviewEnvironmentGitOpsAdoption(t.Context(), source.AccountID, source.ID)
				if err != nil || !preview.CanApply() {
					t.Fatalf("preview: %+v %v", preview, err)
				}
				found := false
				for _, change := range preview.Changes {
					if change.Path == "secret_refs/"+tc.key {
						found = change.Action == "adopt" && bytes.Equal(change.Before, json.RawMessage(`"secret:DATABASE_A"`)) && bytes.Equal(change.After, json.RawMessage(`"secret:DATABASE_B"`))
					}
				}
				if !found {
					t.Fatalf("legacy current names absent from adoption: %+v", preview)
				}
				assertSecretRef(t, store, source, app, "production", tc.key, "")
				if err := store.AdoptEnvironmentGitOps(t.Context(), source.AccountID, source.ID, preview.Hash); err != nil {
					t.Fatal(err)
				}
				assertSecretRef(t, store, source, app, "production", tc.key, "secret:DATABASE_A")
				if err := store.PutAppEnvironmentSecretReference(t.Context(), source.AccountID, app.ID, "production", tc.key, "secret:DATABASE_B"); !errors.Is(err, state.ErrEnvironmentGitManaged) {
					t.Fatalf("legacy adoption did not transfer authority: %v", err)
				}
				lease, err := store.ClaimEnvironmentGitOps(t.Context(), "legacy-reference-worker", time.Now(), time.Minute)
				if err != nil {
					t.Fatal(err)
				}
				plan := claimedIntentPlan(t, store, lease, desired)
				if !plan.CanApply() || !plan.HasDrift() {
					t.Fatalf("approved replacement lost: %+v", plan)
				}
				if _, err := store.(state.EnvironmentGitOpsEffectStore).ApplyEnvironmentGitOpsWithEffects(t.Context(), lease, plan, nil); err != nil {
					t.Fatal(err)
				}
				assertSecretRef(t, store, source, app, "production", tc.key, "secret:DATABASE_B")
				rows, err := store.ListAppSecretsInScope(t.Context(), source.AccountID, app.ID, "production")
				if err != nil || len(rows) != 2 {
					t.Fatalf("adoption altered sealed values: %+v %v", rows, err)
				}
			})
		})
	}
}

func TestEnvironmentGitOpsLegacySecretReferenceReviewBindsDeploymentIdentity(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		store, source, _, app, dep := legacySecretRefFixture(t, basic, json.RawMessage(`{"DATABASE_URL":"secret:DATABASE_A"}`), "DATABASE_URL")
		preview, err := store.PreviewEnvironmentGitOpsAdoption(t.Context(), source.AccountID, source.ID)
		if err != nil || !preview.CanApply() {
			t.Fatalf("preview: %+v %v", preview, err)
		}
		replacement, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Scope: dep.Scope, Kind: dep.Kind, Status: state.DeployLive, ImageDigest: dep.ImageDigest, OverrideEnvSecrets: dep.OverrideEnvSecrets})
		if err != nil || replacement.ID == dep.ID {
			t.Fatalf("replacement: %+v %v", replacement, err)
		}
		if err := store.UpdateDeploymentStatus(t.Context(), dep.ID, state.DeploySuperseded, ""); err != nil {
			t.Fatal(err)
		}
		if err := store.UpdateDeploymentStatus(t.Context(), replacement.ID, state.DeployLive, ""); err != nil {
			t.Fatal(err)
		}
		if err := store.AdoptEnvironmentGitOps(t.Context(), source.AccountID, source.ID, preview.Hash); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("adopted a replaced deployment: %v", err)
		}
		assertSecretRef(t, store, source, app, "production", "DATABASE_URL", "")
		adoptSecretRefs(t, store, source)
	})
}

func TestEnvironmentGitOpsLegacySecretReferenceAmbiguousCanaryBlocks(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		store, source, _, app, _ := legacySecretRefFixture(t, basic, json.RawMessage(`{"DATABASE_URL":"secret:DATABASE_A"}`), "DATABASE_URL")
		canary, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, Status: state.DeployLive, ImageDigest: "sha256:def", CanaryTotalSteps: 2, CanaryStep: 0, RolloutState: "rolling_out", OverrideEnvSecrets: json.RawMessage(`{"DATABASE_URL":"secret:DATABASE_B"}`)})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.UpdateDeploymentStatus(t.Context(), canary.ID, state.DeployLive, ""); err != nil {
			t.Fatal(err)
		}
		preview, err := store.PreviewEnvironmentGitOpsAdoption(t.Context(), source.AccountID, source.ID)
		if err != nil || preview.CanApply() || !strings.Contains(strings.Join(preview.BlockingReasons, " "), "live deployments disagree") {
			t.Fatalf("ambiguous live values qualified: %+v %v", preview, err)
		}
		if err := store.AdoptEnvironmentGitOps(t.Context(), source.AccountID, source.ID, preview.Hash); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("ambiguous adoption: %v", err)
		}
		assertSecretRef(t, store, source, app, "production", "DATABASE_URL", "")
	})
}

func TestEnvironmentSecretReferenceSuppressionProofAndReenable(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		store, source, _, app := secretRefFixture(t, basic, "report")
		// Suppress both an alias and a legacy automatic destination.
		for _, key := range []string{"DATABASE_URL", "DATABASE_A"} {
			if err := store.DeleteAppEnvironmentSecretReference(t.Context(), source.AccountID, app.ID, "production", key); err != nil {
				t.Fatal(err)
			}
		}
		reader := basic.(state.AppEnvironmentSecretIntentReader)
		intent, err := reader.AppEnvironmentSecretIntent(t.Context(), source.AccountID, app.ID, "production")
		if err != nil || !slices.Equal(intent.SuppressedKeys, []string{"DATABASE_A", "DATABASE_URL"}) || len(intent.References) != 0 {
			t.Fatalf("durable absence: %+v %v", intent, err)
		}
		if refs := intent.EffectiveReferences(map[string]string{"DATABASE_URL": "secret:DATABASE_A", "DATABASE_A": "secret:DATABASE_A", "DATABASE_B": "secret:DATABASE_B"}); !maps.Equal(refs, map[string]string{"DATABASE_B": "secret:DATABASE_B"}) {
			t.Fatalf("legacy references reappeared: %+v", refs)
		}
		if required, err := store.RuntimeConfigReceiptRequired(t.Context(), app.ID, "production"); err != nil || !required {
			t.Fatalf("suppression lost required evidence: %v %v", required, err)
		}
		boundary, _, err := state.RuntimeConfigChangedAtForScope(t.Context(), basic, app.ID, "production")
		if err != nil {
			t.Fatal(err)
		}
		a, _ := store.GetAppSecretInScope(t.Context(), source.AccountID, app.ID, "production", "DATABASE_A")
		b, _ := store.GetAppSecretInScope(t.Context(), source.AccountID, app.ID, "production", "DATABASE_B")
		inputs := state.RuntimeConfigInputs{Scope: "production", Boundary: boundary, Variables: map[string]string{}, AllSecrets: true, SecretRefs: map[string]string{"DATABASE_B": "secret:DATABASE_B"}, SecretVersions: map[string]int64{"production/DATABASE_B": b.DeliveryVersion}}
		if fresh, err := store.RuntimeConfigInputsFresh(t.Context(), app.ID, inputs); err != nil || !fresh {
			t.Fatalf("pruned mapping rejected: %v %v", fresh, err)
		}
		// Current timestamps and versions alone cannot prove an old stage-all boot.
		bad := inputs
		bad.SecretRefs = map[string]string{}
		bad.SecretVersions = map[string]int64{"production/DATABASE_A": a.DeliveryVersion, "production/DATABASE_B": b.DeliveryVersion}
		if fresh, err := store.RuntimeConfigInputsFresh(t.Context(), app.ID, bad); err != nil || fresh {
			t.Fatalf("legacy receipt falsely proved removal: %v %v", fresh, err)
		}
		bad = inputs
		bad.SecretRefs = map[string]string{"DATABASE_URL": "secret:DATABASE_B", "DATABASE_B": "secret:DATABASE_B"}
		if fresh, err := store.RuntimeConfigInputsFresh(t.Context(), app.ID, bad); err != nil || fresh {
			t.Fatalf("suppressed destination qualified: %v %v", fresh, err)
		}
		// Selecting the retained source through a different alias is valid.
		if err := store.PutAppEnvironmentSecretReference(t.Context(), source.AccountID, app.ID, "production", "OTHER", "secret:DATABASE_A"); err != nil {
			t.Fatal(err)
		}
		if err := store.PutAppEnvironmentSecretReference(t.Context(), source.AccountID, app.ID, "production", "DATABASE_URL", "secret:DATABASE_B"); err != nil {
			t.Fatal(err)
		}
		intent, err = reader.AppEnvironmentSecretIntent(t.Context(), source.AccountID, app.ID, "production")
		if err != nil || !slices.Equal(intent.SuppressedKeys, []string{"DATABASE_A"}) || intent.References["DATABASE_URL"] != "secret:DATABASE_B" {
			t.Fatalf("reenable: %+v %v", intent, err)
		}
		assertSecretRef(t, store, source, app, "staging", "DATABASE_URL", "secret:DATABASE_A")
	})
}

func TestEnvironmentSecretReferenceClonePreservesSuppressionAndRecreationClearsIt(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		refs, source, _, app := secretRefFixture(t, basic, "report")
		store := refs.(secretRefCloneStore)
		for _, key := range []string{"DATABASE_URL", "DATABASE_A"} {
			if err := store.DeleteAppEnvironmentSecretReference(t.Context(), source.AccountID, app.ID, "production", key); err != nil {
				t.Fatal(err)
			}
		}
		clone := state.ProjectEnvironmentClone{AccountID: source.AccountID, ProjectID: source.ProjectID, SourceSlug: "production", TargetSlug: "preview"}
		created, result, err := store.CloneProjectEnvironment(t.Context(), clone, api.MustLimitsFor(api.PlanPro))
		if err != nil || created.ID == source.EnvironmentID || result.SecretReferencesCopied != 0 {
			t.Fatalf("clone: %+v %+v %v", created, result, err)
		}
		intent, err := basic.(state.AppEnvironmentSecretIntentReader).AppEnvironmentSecretIntent(t.Context(), source.AccountID, app.ID, "preview")
		if err != nil || !slices.Equal(intent.SuppressedKeys, []string{"DATABASE_A", "DATABASE_URL"}) {
			t.Fatalf("clone revived removed keys: %+v %v", intent, err)
		}
		if err := store.PutAppEnvironmentSecretReference(t.Context(), source.AccountID, app.ID, "preview", "DATABASE_URL", "secret:DATABASE_B"); err != nil {
			t.Fatal(err)
		}
		original, _ := basic.(state.AppEnvironmentSecretIntentReader).AppEnvironmentSecretIntent(t.Context(), source.AccountID, app.ID, "production")
		if !slices.Equal(original.SuppressedKeys, []string{"DATABASE_A", "DATABASE_URL"}) {
			t.Fatal("clone shared removal identity")
		}
		if err := store.RollbackProjectEnvironmentClone(t.Context(), source.AccountID, source.ProjectID, "preview"); err != nil {
			t.Fatal(err)
		}
		replacement, err := store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: source.AccountID, ProjectID: source.ProjectID, Slug: "preview"})
		if err != nil || replacement.ID == created.ID {
			t.Fatalf("recreation: %+v %v", replacement, err)
		}
		intent, err = basic.(state.AppEnvironmentSecretIntentReader).AppEnvironmentSecretIntent(t.Context(), source.AccountID, app.ID, "preview")
		if err != nil || len(intent.SuppressedKeys) != 0 {
			t.Fatalf("recreated catalog inherited absence: %+v %v", intent, err)
		}
	})
}

func TestEnvironmentSecretReferenceSuppressionBoundAndCloneQuota(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		store, source, _, app := secretRefFixture(t, basic, "report")
		for i := 0; i < api.EnvironmentSecretReferenceSuppressionsMaxPerApp; i++ {
			if err := store.DeleteAppEnvironmentSecretReference(t.Context(), source.AccountID, app.ID, "production", fmt.Sprintf("REMOVED_%d", i)); err != nil {
				t.Fatal(err)
			}
		}
		if err := store.DeleteAppEnvironmentSecretReference(t.Context(), source.AccountID, app.ID, "production", "ONE_MORE"); !errors.Is(err, state.ErrQuotaExceeded) {
			t.Fatalf("unbounded retained keys: %v", err)
		}
		// An idempotent retry does not spend another slot or advance freshness.
		before, _, err := state.RuntimeConfigChangedAtForScope(t.Context(), basic, app.ID, "production")
		if err != nil {
			t.Fatal(err)
		}
		if err := store.DeleteAppEnvironmentSecretReference(t.Context(), source.AccountID, app.ID, "production", "REMOVED_0"); err != nil {
			t.Fatal(err)
		}
		after, _, err := state.RuntimeConfigChangedAtForScope(t.Context(), basic, app.ID, "production")
		if err != nil || !before.Equal(after) {
			t.Fatalf("idempotent suppression invalidated cache: %v %v %v", before, after, err)
		}
		clone := state.ProjectEnvironmentClone{AccountID: source.AccountID, ProjectID: source.ProjectID, SourceSlug: "production", TargetSlug: "preview"}
		_, _, err = store.(secretRefCloneStore).CloneProjectEnvironment(t.Context(), clone, api.MustLimitsFor(api.PlanPro))
		var quota *state.ProjectEnvironmentCloneQuotaError
		if !errors.As(err, &quota) || quota.Resource != "secret_reference_suppressions" || quota.Observed != 2*api.EnvironmentSecretReferenceSuppressionsMaxPerApp {
			t.Fatalf("clone spent suppression budget twice: %+v %v", quota, err)
		}
		if _, err := store.ProjectEnvironmentBySlug(t.Context(), source.AccountID, source.ProjectID, "preview"); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("failed clone retained catalog: %v", err)
		}
		if err := store.PutAppEnvironmentSecretReference(t.Context(), source.AccountID, app.ID, "production", "REMOVED_0", "secret:DATABASE_B"); err != nil {
			t.Fatal(err)
		}
		if err := store.DeleteAppEnvironmentSecretReference(t.Context(), source.AccountID, app.ID, "production", "ONE_MORE"); err != nil {
			t.Fatalf("reenabling did not reclaim removal slot: %v", err)
		}
	})
}

func TestEnvironmentGitOpsSuppressedSecretRequiresReviewedAdoption(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		store, source, desired, app := secretRefFixture(t, basic, "enforce")
		if err := store.DeleteAppEnvironmentSecretReference(t.Context(), source.AccountID, app.ID, "production", "DATABASE_URL"); err != nil {
			t.Fatal(err)
		}
		lease, err := store.ClaimEnvironmentGitOps(t.Context(), "suppressed-adoption-worker", time.Now(), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		plan := claimedIntentPlan(t, store, lease, desired)
		if plan.CanApply() || !strings.Contains(strings.Join(plan.BlockingReasons, " "), "adoption required") {
			t.Fatalf("explicit unowned absence was overwritten without adoption: %+v", plan)
		}
		preview, err := store.PreviewEnvironmentGitOpsAdoption(t.Context(), source.AccountID, source.ID)
		if err != nil || !preview.CanApply() {
			t.Fatalf("adoption: %+v %v", preview, err)
		}
		found := false
		for _, change := range preview.Changes {
			if change.Path == "secret_refs/DATABASE_URL" {
				found = change.Action == "adopt" && bytes.Equal(change.Before, json.RawMessage("null"))
			}
		}
		if !found {
			t.Fatalf("suppression absent from review: %+v", preview)
		}
		if err := store.AdoptEnvironmentGitOps(t.Context(), source.AccountID, source.ID, preview.Hash); err != nil {
			t.Fatal(err)
		}
		intent, err := basic.(state.AppEnvironmentSecretIntentReader).AppEnvironmentSecretIntent(t.Context(), source.AccountID, app.ID, "production")
		if err != nil || !slices.Equal(intent.SuppressedKeys, []string{"DATABASE_URL"}) || len(intent.References) != 0 {
			t.Fatalf("adoption changed absence: %+v %v", intent, err)
		}
		if err := store.PutAppEnvironmentSecretReference(t.Context(), source.AccountID, app.ID, "production", "DATABASE_URL", "secret:DATABASE_A"); !errors.Is(err, state.ErrEnvironmentGitManaged) {
			t.Fatalf("adopted absence lost ownership: %v", err)
		}
		plan = claimedIntentPlan(t, store, lease, desired)
		if !plan.CanApply() || !plan.HasDrift() {
			t.Fatalf("approved reenable: %+v", plan)
		}
		if _, err := store.(state.EnvironmentGitOpsEffectStore).ApplyEnvironmentGitOpsWithEffects(t.Context(), lease, plan, nil); err != nil {
			t.Fatal(err)
		}
		intent, err = basic.(state.AppEnvironmentSecretIntentReader).AppEnvironmentSecretIntent(t.Context(), source.AccountID, app.ID, "production")
		if err != nil || len(intent.SuppressedKeys) != 0 || intent.References["DATABASE_URL"] != "secret:DATABASE_B" {
			t.Fatalf("reviewed reenable: %+v %v", intent, err)
		}
	})
}

func TestEnvironmentSecretReferenceCatalogRemovalInvalidatesCapturedIntent(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		store, source, _, app := secretRefFixture(t, basic, "report")
		env, err := store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: source.AccountID, ProjectID: source.ProjectID, Slug: "empty"})
		if err != nil {
			t.Fatal(err)
		}
		// No sealed-secret deletion can supply an incidental freshness stamp.
		if err := store.DeleteAppEnvironmentSecretReference(t.Context(), source.AccountID, app.ID, env.Slug, "REMOVED"); err != nil {
			t.Fatal(err)
		}
		dep, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Scope: env.Slug, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.CreateSnapshot(t.Context(), state.Snapshot{DeploymentID: dep.ID, FCVersion: "1.13.0", StorageKey: state.SnapMemKey(dep.ID)}); err != nil {
			t.Fatal(err)
		}
		before, _, err := state.RuntimeConfigChangedAtForScope(t.Context(), basic, app.ID, env.Slug)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.DeleteProjectEnvironment(t.Context(), source.AccountID, source.ProjectID, env.Slug); err != nil {
			t.Fatal(err)
		}
		after, _, err := state.RuntimeConfigChangedAtForScope(t.Context(), basic, app.ID, env.Slug)
		if err != nil || !after.After(before) {
			t.Fatalf("catalog cascade lost freshness boundary: %v %v %v", before, after, err)
		}
		if _, err := store.LatestSnapshot(t.Context(), dep.ID); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("catalog cascade left a restorable cache: %v", err)
		}
		replacement, err := store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: source.AccountID, ProjectID: source.ProjectID, Slug: env.Slug})
		if err != nil || replacement.ID == env.ID {
			t.Fatalf("replacement: %+v %v", replacement, err)
		}
		intent, err := basic.(state.AppEnvironmentSecretIntentReader).AppEnvironmentSecretIntent(t.Context(), source.AccountID, app.ID, env.Slug)
		if err != nil || len(intent.SuppressedKeys) != 0 || len(intent.References) != 0 {
			t.Fatalf("replacement inherited old intent: %+v %v", intent, err)
		}
	})
}
