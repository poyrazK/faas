package pgintegration_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

type gitOpsTestStore interface {
	state.Store
	state.EnvironmentGitOpsStore
}

func stores(t *testing.T, run func(*testing.T, gitOpsTestStore)) {
	t.Helper()
	t.Run("memory", func(t *testing.T) { run(t, state.NewMemStore()) })
	t.Run("postgres", func(t *testing.T) {
		if os.Getenv("GREGALE_GITOPS_ACCEPTANCE") == "1" {
			if os.Getenv("DATABASE_URL") == "" || os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
				t.Fatal("GitOps acceptance requires DATABASE_URL and enabled PostgreSQL tests")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			check, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
			if err != nil {
				t.Fatal("GitOps acceptance database configuration is invalid")
			}
			defer check.Close()
			if err := check.Ping(ctx); err != nil {
				t.Fatal("GitOps acceptance database is unreachable")
			}
		}
		pool := pgtest.OpenMigrated(t)
		if err := db.MigrateUp(context.Background(), pool); err != nil {
			t.Fatal(err)
		}
		run(t, state.NewPgStore(pool))
	})
}

func seed(t *testing.T, store gitOpsTestStore) (state.EnvironmentGitSource, environmentsync.DesiredState) {
	return seedMode(t, store, "report")
}

func seedMode(t *testing.T, store gitOpsTestStore, mode string) (state.EnvironmentGitSource, environmentsync.DesiredState) {
	t.Helper()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, "gitops@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "shop", RepoFullName: "example/shop", ProductionBranch: "main", InstallID: 42})
	if err != nil {
		t.Fatal(err)
	}
	source, err := store.CreateEnvironmentGitSource(ctx, account.ID, project.ID, "production", state.EnvironmentGitSourceSpec{RepositoryID: 123, InstallationID: 42, Repository: "example/shop", Ref: "refs/heads/main", ManifestPath: "environments/production.yaml", Mode: mode, ApprovalPolicy: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	desired, err := environmentsync.Compile(api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion, Project: "shop", Environment: "production", Workloads: map[string]api.EnvironmentWorkload{"api": {App: "shop-api", Variables: map[string]string{"MODE": "production"}}}})
	if err != nil {
		t.Fatal(err)
	}
	return source, desired
}

func approval(source state.EnvironmentGitSource, desired environmentsync.DesiredState, sha string) state.ApproveEnvironmentRevision {
	return state.ApproveEnvironmentRevision{AccountID: source.AccountID, SourceID: source.ID, ExpectedGeneration: source.Generation, CommitSHA: sha, Desired: desired, ApprovedBy: "account-owner"}
}

func TestEnvironmentGitOpsApprovalAndScope(t *testing.T) {
	stores(t, func(t *testing.T, store gitOpsTestStore) {
		ctx := context.Background()
		source, desired := seed(t, store)
		if _, err := store.EnvironmentGitSource(ctx, "00000000-0000-0000-0000-000000000000", source.ProjectID, source.EnvironmentSlug); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("cross-account source lookup: %v", err)
		}
		if _, err := store.CreateEnvironmentGitSource(ctx, source.AccountID, source.ProjectID, source.EnvironmentSlug, source.Spec); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("second source permitted: %v", err)
		}
		if _, err := store.ClaimEnvironmentGitOps(ctx, "before-approval", time.Now(), time.Minute); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("unapproved source executed: %v", err)
		}
		input := approval(source, desired, strings.Repeat("a", 40))
		updated, revision, err := store.ApproveEnvironmentDesiredRevision(ctx, input)
		if err != nil || updated.Generation != 1 || updated.ApprovedRevisionID != revision.ID || updated.AppliedRevisionID != "" {
			t.Fatalf("approval result: %+v %+v %v", updated, revision, err)
		}
		if _, _, err := store.ApproveEnvironmentDesiredRevision(ctx, input); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("stale generation accepted: %v", err)
		}
		input.ExpectedGeneration = updated.Generation
		repeated, same, err := store.ApproveEnvironmentDesiredRevision(ctx, input)
		if err != nil || repeated.Generation != 1 || same.ID != revision.ID {
			t.Fatalf("approval retry changed identity: %+v %+v %v", repeated, same, err)
		}
		input.Desired.Definition.Environment = "staging"
		input.Desired, _ = environmentsync.Compile(input.Desired.Definition)
		if _, _, err := store.ApproveEnvironmentDesiredRevision(ctx, input); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("definition applied to wrong environment: %v", err)
		}
	})
}

func TestEnvironmentGitOpsSupersededAndExpiredLeasesCannotFinish(t *testing.T) {
	stores(t, func(t *testing.T, store gitOpsTestStore) {
		ctx := context.Background()
		source, desired := seed(t, store)
		source, _, err := store.ApproveEnvironmentDesiredRevision(ctx, approval(source, desired, strings.Repeat("a", 40)))
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC().Add(time.Second)
		old, err := store.ClaimEnvironmentGitOps(ctx, "old-worker", now, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.ClaimEnvironmentGitOps(ctx, "other-worker", now, time.Minute); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("two active claims: %v", err)
		}
		source, _, err = store.ApproveEnvironmentDesiredRevision(ctx, approval(source, desired, strings.Repeat("b", 40)))
		if err != nil {
			t.Fatal(err)
		}
		if err := store.RenewEnvironmentGitOps(ctx, old, now, time.Minute); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("superseded lease renewed: %v", err)
		}
		if err := store.FinishEnvironmentGitOps(ctx, old, "drifted", json.RawMessage(`{}`), json.RawMessage(`[]`), "", now, now.Add(time.Minute)); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("superseded worker published result: %v", err)
		}
		current, err := store.ClaimEnvironmentGitOps(ctx, "new-worker", now, time.Minute)
		if err != nil || current.Source.Generation != source.Generation {
			t.Fatalf("new generation waited for stale lease: %+v %v", current, err)
		}
		expiredAt := now.Add(time.Minute)
		if err := store.RenewEnvironmentGitOps(ctx, current, expiredAt, time.Minute); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("expired lease renewed: %v", err)
		}
		recovered, err := store.ClaimEnvironmentGitOps(ctx, "recovery-worker", expiredAt, time.Minute)
		if err != nil || recovered.AttemptCount != 3 {
			t.Fatalf("crashed worker not recovered: %+v %v", recovered, err)
		}
		if err := store.FinishEnvironmentGitOps(ctx, current, "drifted", json.RawMessage(`{}`), json.RawMessage(`[]`), "", expiredAt, expiredAt.Add(time.Minute)); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("replaced token published result: %v", err)
		}
		runs, err := store.ListEnvironmentGitOpsRuns(ctx, source.AccountID, source.ID, 10)
		if err != nil || len(runs) != 3 {
			t.Fatalf("missing history: %+v %v", runs, err)
		}
		superseded := 0
		for _, run := range runs {
			if run.Status == "superseded" && run.CompletedAt != nil {
				superseded++
			}
		}
		if superseded != 2 {
			t.Fatalf("crashed/superseded attempts hidden: %+v", runs)
		}
	})
}

func TestEnvironmentGitOpsCompetingClaimsAndPeriodicWork(t *testing.T) {
	stores(t, func(t *testing.T, store gitOpsTestStore) {
		ctx := context.Background()
		source, desired := seed(t, store)
		source, _, err := store.ApproveEnvironmentDesiredRevision(ctx, approval(source, desired, strings.Repeat("a", 40)))
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC().Add(time.Second)
		var wg sync.WaitGroup
		results := make(chan state.EnvironmentGitOpsLease, 2)
		errorsCh := make(chan error, 2)
		for _, token := range []string{"worker-one", "worker-two"} {
			wg.Add(1)
			go func(token string) {
				defer wg.Done()
				lease, err := store.ClaimEnvironmentGitOps(ctx, token, now, time.Minute)
				if err == nil {
					results <- lease
				} else {
					errorsCh <- err
				}
			}(token)
		}
		wg.Wait()
		if len(results) != 1 || len(errorsCh) != 1 || !errors.Is(<-errorsCh, state.ErrNotFound) {
			t.Fatalf("competing workers not serialized: successes=%d errors=%d", len(results), len(errorsCh))
		}
		lease := <-results
		if err := store.FinishEnvironmentGitOps(ctx, lease, "converged", json.RawMessage(`{}`), json.RawMessage(`[]`), "", now, now.Add(time.Minute)); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("unverified convergence accepted: %v", err)
		}
		owners := make([]environmentsync.Ownership, 0, len(desired.Fields))
		for _, field := range desired.Fields {
			owners = append(owners, environmentsync.Ownership{Field: field, Manager: source.ID})
		}
		plan, err := environmentsync.BuildPlan(desired, environmentsync.ObservedState{Fields: desired.Fields}, owners, environmentsync.PlanOptions{Manager: source.ID, Revision: lease.Revision.ID, Generation: source.Generation})
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(plan)
		next := now.Add(30 * time.Second)
		if err := store.FinishEnvironmentGitOps(ctx, lease, "converged", raw, json.RawMessage(`[]`), "", now, next); err != nil {
			t.Fatal(err)
		}
		status, err := store.EnvironmentGitSource(ctx, source.AccountID, source.ProjectID, source.EnvironmentSlug)
		if err != nil || status.AppliedRevisionID != lease.Revision.ID {
			t.Fatalf("verified revision not recorded: %+v %v", status, err)
		}
		if _, err := store.ClaimEnvironmentGitOps(ctx, "early-check", next.Add(-time.Millisecond), time.Minute); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("periodic work ran early: %v", err)
		}
		if _, err := store.ClaimEnvironmentGitOps(ctx, "periodic-check", next, time.Minute); err != nil {
			t.Fatalf("periodic check not durable: %v", err)
		}
		cross, err := store.ListEnvironmentGitOpsRuns(ctx, "00000000-0000-0000-0000-000000000000", source.ID, 10)
		if err != nil || len(cross) != 0 {
			t.Fatalf("cross-account history exposed: %+v %v", cross, err)
		}
	})
}
