package pgintegration_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestEnvironmentGitOpsExternalOwnershipPreventsAdoption(t *testing.T) {
	stores(t, func(t *testing.T, base gitOpsTestStore) {
		store := base.(intentTestStore)
		source, _, app := intentFixture(t, store, "report")
		values, hash, err := api.NormalizeProjectEnvironmentConfig([]byte(`{"LOG_LEVEL":"console"}`))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = store.CreateProjectEnvironmentConfigVersion(t.Context(), state.ProjectEnvironmentConfig{AccountID: source.AccountID, ProjectID: source.ProjectID, EnvironmentSlug: source.EnvironmentSlug, Values: values, ConfigHash: hash}); err != nil {
			t.Fatal(err)
		}
		owners := base.(state.EnvironmentFieldOwnershipStore)
		request := api.EnvironmentFieldOwnershipRequest{App: app.Slug, Environment: source.EnvironmentSlug, Paths: []string{"variables/MODE"}, Manager: "terraform"}
		if scoped, err := owners.SetEnvironmentFieldOwnership(t.Context(), source.AccountID, request, false); err != nil || !scoped {
			t.Fatalf("claim: %v %v", scoped, err)
		}
		plan, err := store.PreviewEnvironmentGitOpsAdoption(t.Context(), source.AccountID, source.ID)
		if err != nil || plan.CanApply() {
			t.Fatalf("foreign owner permitted adoption: %+v %v", plan, err)
		}
		blockedHash := plan.Hash
		if _, err = owners.SetEnvironmentFieldOwnership(t.Context(), source.AccountID, request, false); err != nil {
			t.Fatal(err)
		}
		repeated, err := store.PreviewEnvironmentGitOpsAdoption(t.Context(), source.AccountID, source.ID)
		if err != nil || repeated.Hash != blockedHash {
			t.Fatalf("idempotent claim changed plan: %+v %v", repeated, err)
		}
		if _, err = owners.SetEnvironmentFieldOwnership(t.Context(), source.AccountID, request, true); err != nil {
			t.Fatal(err)
		}
		plan, err = store.PreviewEnvironmentGitOpsAdoption(t.Context(), source.AccountID, source.ID)
		if err != nil || !plan.CanApply() {
			t.Fatalf("release: %+v %v", plan, err)
		}
		if err = store.AdoptEnvironmentGitOps(t.Context(), source.AccountID, source.ID, plan.Hash); err != nil {
			t.Fatal(err)
		}
		if _, err = owners.SetEnvironmentFieldOwnership(t.Context(), source.AccountID, request, false); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("Terraform took Git-owned report field: %v", err)
		}
		request.App = ""
		request.Project = "shop"
		request.Paths = []string{"configuration/LOG_LEVEL"}
		if _, err = owners.SetEnvironmentFieldOwnership(t.Context(), source.AccountID, request, false); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("Terraform took Git configuration: %v", err)
		}
		request.Environment = "not-registered"
		// Unregistered scopes cannot create ownership for the production environment.
		if _, err = owners.SetEnvironmentFieldOwnership(t.Context(), source.AccountID, request, false); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("unregistered scope: %v", err)
		}
	})
}

func TestEnvironmentGitOpsExternalOwnershipRacingAdoption(t *testing.T) {
	stores(t, func(t *testing.T, base gitOpsTestStore) {
		store := base.(intentTestStore)
		source, _, app := intentFixture(t, store, "report")
		plan, err := store.PreviewEnvironmentGitOpsAdoption(t.Context(), source.AccountID, source.ID)
		if err != nil || !plan.CanApply() {
			t.Fatalf("preview: %+v %v", plan, err)
		}
		start, results := make(chan struct{}), make(chan error, 2)
		go func() {
			<-start
			_, err := base.(state.EnvironmentFieldOwnershipStore).SetEnvironmentFieldOwnership(t.Context(), source.AccountID, api.EnvironmentFieldOwnershipRequest{App: app.Slug, Environment: source.EnvironmentSlug, Paths: []string{"variables/MODE"}, Manager: "terraform"}, false)
			results <- err
		}()
		go func() {
			<-start
			results <- store.AdoptEnvironmentGitOps(t.Context(), source.AccountID, source.ID, plan.Hash)
		}()
		close(start)
		successes := 0
		for range 2 {
			if err := <-results; err == nil {
				successes++
			} else if !errors.Is(err, state.ErrConflict) {
				t.Fatalf("unexpected concurrent ownership error: %v", err)
			}
		}
		if successes != 1 {
			t.Fatalf("concurrent writers acquired conflicting ownership: %d successes", successes)
		}
	})
}

func TestPgEnvironmentGitOpsRebindingLocksEnvironmentBeforeSource(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	source, _, _ := intentFixture(t, store, "report")
	q := sqlc.New()
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err = q.LockEnvironmentGitOpsEnvironment(t.Context(), tx, sqlc.LockEnvironmentGitOpsEnvironmentParams{AccountID: auditPgUUID(t, source.AccountID), ProjectID: auditPgUUID(t, source.ProjectID), Environment: source.EnvironmentSlug}); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		spec := source.Spec
		spec.ManifestPath = "env/rebound.yaml"
		_, err := store.RebindEnvironmentGitSource(t.Context(), source.AccountID, source.ID, source.Generation, spec)
		result <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		if err = pool.QueryRow(t.Context(), "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock')").Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("rebinding did not wait for the environment lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// A source-first implementation holds this row while waiting on the
	// environment and can deadlock with a concurrent binding/ownership claim.
	probe, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = probe.Rollback(t.Context()) }()
	if _, err = probe.Exec(t.Context(), "SELECT id FROM environment_git_sources WHERE id=$1 FOR UPDATE NOWAIT", source.ID); err != nil {
		t.Fatalf("rebinding locked the source before the environment: %v", err)
	}
	if err = probe.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = <-result; err != nil {
		t.Fatalf("rebind after releasing environment: %v", err)
	}
}

func auditPgUUID(t *testing.T, value string) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	if err := id.Scan(value); err != nil {
		t.Fatal(err)
	}
	return id
}
func TestEnvironmentGitOpsRebindingFencesApprovalAndPreservesValues(t *testing.T) {
	stores(t, func(t *testing.T, base gitOpsTestStore) {
		store := base.(intentTestStore)
		source, desired, app := intentFixture(t, store, "report")
		plan, err := store.PreviewEnvironmentGitOpsAdoption(t.Context(), source.AccountID, source.ID)
		if err != nil || !plan.CanApply() {
			t.Fatalf("preview: %v %+v", err, plan)
		}
		if err = store.AdoptEnvironmentGitOps(t.Context(), source.AccountID, source.ID, plan.Hash); err != nil {
			t.Fatal(err)
		}
		lease, err := store.ClaimEnvironmentGitOps(t.Context(), "retired-lease", time.Now(), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		lifecycle := base.(state.EnvironmentGitOpsLifecycleStore)
		spec := source.Spec
		spec.ManifestPath = "env/new.yaml"
		spec.Ref = "refs/heads/staging"
		if _, err = lifecycle.RebindEnvironmentGitSource(t.Context(), source.AccountID, source.ID, source.Generation-1, spec); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("stale rebind: %v", err)
		}
		invalid := spec
		invalid.Repository = "other/shop"
		if _, err = lifecycle.RebindEnvironmentGitSource(t.Context(), source.AccountID, source.ID, source.Generation, invalid); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("unverified repository accepted: %v", err)
		}
		next, err := lifecycle.RebindEnvironmentGitSource(t.Context(), source.AccountID, source.ID, source.Generation, spec)
		if err != nil || next.ID == source.ID || next.Generation <= source.Generation || next.ApprovedRevisionID != "" || next.AppliedRevisionID != "" || next.SourceVerifiedAt != nil {
			t.Fatalf("rebound: %+v %v", next, err)
		}
		assertVariable(t, store, source, app, "production", "MODE", "console")
		assertVariable(t, store, source, app, "production", "MANUAL", "keep")
		if _, err = store.ObserveEnvironmentGitOps(t.Context(), lease, desired); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("retired lease remained authoritative: %v", err)
		}
		if _, err = store.ClaimEnvironmentGitOps(t.Context(), "unapproved-new-binding", time.Now(), time.Minute); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("old approval carried into new binding: %v", err)
		}
		if err = store.AdoptEnvironmentGitOps(t.Context(), source.AccountID, source.ID, plan.Hash); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("retired plan: %v", err)
		}
		history, err := store.ListEnvironmentGitOpsRuns(t.Context(), source.AccountID, source.ID, 20)
		if err != nil || len(history) != 1 {
			t.Fatalf("retired history lost: %+v %v", history, err)
		}
		if _, err = store.UpdateEnvironmentGitSource(t.Context(), source.AccountID, source.ID, api.EnvironmentGitSourceUpdate{ExpectedGeneration: next.Generation - 1, Suspended: boolPointer(false)}); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("retired source unsuspended: %v", err)
		}
		if err = lifecycle.DetachEnvironmentGitSource(t.Context(), source.AccountID, next.ID, next.Generation); err != nil {
			t.Fatal(err)
		}
		if _, err = store.EnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, source.EnvironmentSlug); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("detached source exposed as current: %v", err)
		}
		fresh, err := store.CreateEnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, source.EnvironmentSlug, spec)
		if err != nil || fresh.Generation <= next.Generation {
			t.Fatalf("reconnect generation reused: %+v %v", fresh, err)
		}
	})
}
func boolPointer(v bool) *bool { return &v }
func TestEnvironmentGitOpsCanaryChecksOnlyManagedSecretKeys(t *testing.T) {
	stores(t, func(t *testing.T, base gitOpsTestStore) {
		store := base.(intentTestStore)
		source, desired, app := intentFixture(t, store, "report")
		for _, name := range []string{"SOURCE_A", "SOURCE_B"} {
			if err := store.UpsertAppSecretInScope(t.Context(), source.AccountID, app.ID, source.EnvironmentSlug, name, []byte("sealed")); err != nil {
				t.Fatal(err)
			}
			refs, _ := json.Marshal(map[string]string{"DATABASE_URL": "secret:" + name})
			if dep, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Scope: source.EnvironmentSlug, Kind: state.DeploymentKindImage, ImageDigest: "registry.example/shop@sha256:" + strings.Repeat("b", 64), Status: state.DeployLive, CanaryTotalSteps: 2, CanaryStep: 0, RolloutState: "rolling_out", OverrideEnvSecrets: refs}); err != nil {
				t.Fatal(err)
			} else if err = store.UpdateDeploymentStatus(t.Context(), dep.ID, state.DeployLive, ""); err != nil {
				t.Fatal(err)
			}
		}
		plan, err := store.PreviewEnvironmentGitOpsAdoption(t.Context(), source.AccountID, source.ID)
		if err != nil || !plan.CanApply() {
			t.Fatalf("unmanaged canary refs blocked variable adoption: %+v %v", plan, err)
		}
		workload := desired.Definition.Workloads["api"]
		workload.SecretRefs = map[string]string{"DATABASE_URL": "secret:SOURCE_A"}
		desired.Definition.Workloads["api"] = workload
		desired, err = environmentsync.Compile(desired.Definition)
		if err != nil {
			t.Fatal(err)
		}
		source, _, err = store.ApproveEnvironmentDesiredRevision(t.Context(), approval(source, desired, strings.Repeat("c", 40)))
		if err != nil {
			t.Fatal(err)
		}
		plan, err = store.PreviewEnvironmentGitOpsAdoption(t.Context(), source.AccountID, source.ID)
		if err != nil || plan.CanApply() {
			t.Fatalf("managed canary disagreement accepted: %+v %v", plan, err)
		}
	})
}
func TestEnvironmentGitOpsCompletedReportHistoryIsBounded(t *testing.T) {
	stores(t, func(t *testing.T, store gitOpsTestStore) {
		source, desired := seed(t, store)
		source, _, err := store.ApproveEnvironmentDesiredRevision(t.Context(), approval(source, desired, strings.Repeat("a", 40)))
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now().Add(time.Second)
		for i := 0; i < api.EnvironmentGitOpsReportRunsMaxPerSource+3; i++ {
			at := now.Add(time.Duration(i) * time.Second)
			lease, err := store.ClaimEnvironmentGitOps(t.Context(), fmt.Sprint("report-", i), at, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if err = store.FinishEnvironmentGitOps(t.Context(), lease, "drifted", json.RawMessage(`{}`), json.RawMessage(`[]`), "", at, at); err != nil {
				t.Fatal(err)
			}
		}
		runs, err := store.ListEnvironmentGitOpsRuns(t.Context(), source.AccountID, source.ID, api.EnvironmentGitOpsReportRunsMaxPerSource+10)
		if err != nil || len(runs) != api.EnvironmentGitOpsReportRunsMaxPerSource {
			t.Fatalf("count bound: %d %v", len(runs), err)
		}
		later := now.Add(api.EnvironmentGitOpsReportRetention + time.Hour)
		lease, err := store.ClaimEnvironmentGitOps(t.Context(), "later-report", later, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if err = store.FinishEnvironmentGitOps(t.Context(), lease, "drifted", json.RawMessage(`{}`), json.RawMessage(`[]`), "", later, later); err != nil {
			t.Fatal(err)
		}
		runs, err = store.ListEnvironmentGitOpsRuns(t.Context(), source.AccountID, source.ID, 20)
		if err != nil || len(runs) != 1 || runs[0].ID != lease.RunID {
			t.Fatalf("age bound: %+v %v", runs, err)
		}
	})
}

func TestEnvironmentGitOpsOwnershipBeforeBindingAndProtectedRebind(t *testing.T) {
	stores(t, func(t *testing.T, base gitOpsTestStore) {
		store := base.(intentTestStore)
		source, desired, app := intentFixture(t, store, "report")
		lifecycle := base.(state.EnvironmentGitOpsLifecycleStore)
		if err := lifecycle.DetachEnvironmentGitSource(t.Context(), source.AccountID, source.ID, source.Generation); err != nil {
			t.Fatal(err)
		}
		owners := base.(state.EnvironmentFieldOwnershipStore)
		req := api.EnvironmentFieldOwnershipRequest{App: app.Slug, Environment: source.EnvironmentSlug, Paths: []string{"variables/MODE"}, Manager: "terraform"}
		if scoped, err := owners.SetEnvironmentFieldOwnership(t.Context(), source.AccountID, req, false); err != nil || !scoped {
			t.Fatalf("claim before binding: %v %v", scoped, err)
		}
		source, err := store.CreateEnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, source.EnvironmentSlug, source.Spec)
		if err != nil {
			t.Fatal(err)
		}
		source, _, err = store.ApproveEnvironmentDesiredRevision(t.Context(), approval(source, desired, strings.Repeat("a", 40)))
		if err != nil {
			t.Fatal(err)
		}
		plan, err := store.PreviewEnvironmentGitOpsAdoption(t.Context(), source.AccountID, source.ID)
		if err != nil || plan.CanApply() {
			t.Fatalf("pre-existing foreign owner ignored: %+v %v", plan, err)
		}
		spec := source.Spec
		spec.ApprovalPolicy = "protected_branch"
		protected, err := lifecycle.RebindEnvironmentGitSource(t.Context(), source.AccountID, source.ID, source.Generation, spec)
		if err != nil {
			t.Fatal(err)
		}
		spec.ManifestPath = "env/reviewed.yaml"
		next, err := lifecycle.RebindEnvironmentGitSource(t.Context(), source.AccountID, protected.ID, protected.Generation, spec)
		if err != nil || next.Spec.ApprovalPolicy != "protected_branch" || next.ApprovedRevisionID != "" {
			t.Fatalf("protected rebind: %+v %v", next, err)
		}
		req.Project = "other-project"
		if _, err = owners.SetEnvironmentFieldOwnership(t.Context(), source.AccountID, req, false); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("ambiguous scope accepted: %v", err)
		}
	})
}

func TestEnvironmentGitOpsAbandonedReportHistoryIsBounded(t *testing.T) {
	stores(t, func(t *testing.T, store gitOpsTestStore) {
		source, desired := seed(t, store)
		source, _, err := store.ApproveEnvironmentDesiredRevision(t.Context(), approval(source, desired, strings.Repeat("a", 40)))
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now().Add(time.Second)
		var active state.EnvironmentGitOpsLease
		for i := 0; i < api.EnvironmentGitOpsReportRunsMaxPerSource+3; i++ {
			active, err = store.ClaimEnvironmentGitOps(t.Context(), fmt.Sprint("crashed-", i), now.Add(time.Duration(i)*time.Second), time.Millisecond)
			if err != nil {
				t.Fatal(err)
			}
		}
		runs, err := store.ListEnvironmentGitOpsRuns(t.Context(), source.AccountID, source.ID, api.EnvironmentGitOpsReportRunsMaxPerSource+10)
		if err != nil || len(runs) != api.EnvironmentGitOpsReportRunsMaxPerSource+1 {
			t.Fatalf("abandoned run bound: %d %v", len(runs), err)
		}
		if runs[0].ID != active.RunID || runs[0].CompletedAt != nil {
			t.Fatalf("active run was pruned: %+v", runs[0])
		}
	})
}
