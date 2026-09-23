package state

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemStoreProjectEnvironmentCleanupJobCanBeRetried(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	account, err := store.CreateAccount(ctx, "environment-cleanup@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, Project{AccountID: account.ID, Slug: "cleanup-shop", ScanSource: ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(ctx, ProjectEnvironment{
		AccountID: account.ID, ProjectID: project.ID, Slug: "staging",
	}); err != nil {
		t.Fatal(err)
	}
	resources := ProjectEnvironmentCleanupResources{Postgres: []ProjectEnvironmentPostgresCleanupResource{{
		AppID: "app-id", Scope: "staging", BindingID: "binding-id", DatabaseID: "database-id",
		DatabaseName: "env-staging-0123456789ab", RestoreSourceDatabaseID: "source-database-id", DeleteDatabase: true,
	}}}
	job, err := store.DeleteProjectEnvironmentWithCleanup(ctx, account.ID, project.ID, "staging", resources, "request-lease", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if job.ID == "" || job.LeaseToken != "request-lease" || job.Resources.Postgres[0].DatabaseID != "database-id" {
		t.Fatalf("cleanup job=%+v", job)
	}
	if _, err := store.ProjectEnvironmentBySlug(ctx, account.ID, project.ID, "staging"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("environment remains after delete: %v", err)
	}

	claimAt := job.LeaseUntil.Add(time.Second)
	claimed, err := store.ClaimNextProjectEnvironmentCleanup(ctx, "worker-lease-1", claimAt, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claimed.ID != job.ID || claimed.AttemptCount != 1 || claimed.LeaseToken != "worker-lease-1" {
		t.Fatalf("claimed job=%+v", claimed)
	}
	nextAttempt := claimAt.Add(time.Minute)
	if err := store.RetryProjectEnvironmentCleanup(ctx, claimed.ID, "wrong-lease", nextAttempt); !errors.Is(err, ErrConflict) {
		t.Fatalf("retry with wrong lease = %v, want conflict", err)
	}
	if err := store.RetryProjectEnvironmentCleanup(ctx, claimed.ID, claimed.LeaseToken, nextAttempt); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimNextProjectEnvironmentCleanup(ctx, "worker-too-early", nextAttempt.Add(-time.Second), time.Minute); !errors.Is(err, ErrNotFound) {
		t.Fatalf("early claim = %v, want not found", err)
	}
	claimed, err = store.ClaimNextProjectEnvironmentCleanup(ctx, "worker-lease-2", nextAttempt, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claimed.AttemptCount != 2 || claimed.LeaseToken != "worker-lease-2" {
		t.Fatalf("second claim=%+v", claimed)
	}
	if err := store.CompleteProjectEnvironmentCleanup(ctx, claimed.ID, claimed.LeaseToken); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimNextProjectEnvironmentCleanup(ctx, "worker-lease-3", nextAttempt.Add(time.Hour), time.Minute); !errors.Is(err, ErrNotFound) {
		t.Fatalf("claim after completion = %v, want not found", err)
	}
}

func TestProjectEnvironmentCleanupResourcesValidateScopeAndCloneIdentity(t *testing.T) {
	valid := ProjectEnvironmentCleanupResources{Postgres: []ProjectEnvironmentPostgresCleanupResource{{
		AppID: "app", Scope: "staging", BindingID: "binding", DatabaseID: "database",
		DatabaseName: "env-staging-hash", RestoreSourceDatabaseID: "source", DeleteDatabase: true,
	}}}
	if err := valid.ValidateForEnvironment("staging"); err != nil {
		t.Fatalf("valid resources rejected: %v", err)
	}
	valid.Postgres[0].Scope = "production"
	if err := valid.ValidateForEnvironment("staging"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("cross-scope cleanup resources = %v, want invalid argument", err)
	}
	valid.Postgres[0].Scope = "staging"
	valid.Postgres[0].RestoreSourceDatabaseID = ""
	if err := valid.ValidateForEnvironment("staging"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("unowned database deletion = %v, want invalid argument", err)
	}
}
