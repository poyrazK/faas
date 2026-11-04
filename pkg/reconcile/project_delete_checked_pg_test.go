//go:build !no_pg

package reconcile_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPgProjectDeletionWaitsForStageWriter(t *testing.T) {
	store, _, pool, _ := pgReconcileStore(t)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	acct, err := store.CreateAccount(ctx, "delete-lock@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "delete-lock"})
	if err != nil {
		t.Fatal(err)
	}
	accountID := pgtype.UUID{Bytes: uuid.MustParse(acct.ID), Valid: true}
	projectID := pgtype.UUID{Bytes: uuid.MustParse(project.ID), Valid: true}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := sqlc.New()
	if _, err := q.LockProjectEnvironmentCloneProject(ctx, tx, sqlc.LockProjectEnvironmentCloneProjectParams{AccountID: accountID, ProjectID: projectID}); err != nil {
		t.Fatal(err)
	}
	// Hold the same lock as CreateProjectEnvironment before publishing its row.
	started, done := make(chan struct{}), make(chan error, 1)
	go func() {
		close(started)
		done <- store.DeleteProjectIfNoStages(ctx, acct.ID, project.ID)
	}()
	<-started
	select {
	case err := <-done:
		t.Fatalf("delete bypassed the stage writer's lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if _, err := q.CreateTrafficProjectEnvironment(ctx, tx, sqlc.CreateTrafficProjectEnvironmentParams{AccountID: accountID, ProjectID: projectID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, state.ErrConflict) {
			t.Fatalf("delete did not observe committed stage: %v", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if _, err := store.ProjectEnvironmentBySlug(ctx, acct.ID, project.ID, "staging"); err != nil {
		t.Fatal(err)
	}
}
