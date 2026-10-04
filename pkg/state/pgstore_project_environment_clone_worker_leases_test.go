//go:build !no_pg

// adr: 569
package state_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgProjectEnvironmentCloneWorkerLeaseOwnership(t *testing.T) {
	s, _, _ := pgWithPool(t)
	projectEnvironmentCloneWorkerLeaseOwnership(t, s)
}

func TestPgProjectEnvironmentCloneWorkerSkipsLockedProject(t *testing.T) {
	s, ctx, pool := pgWithPool(t)
	a, err := s.CreateAccount(ctx, "locked-clone@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	var projects []state.Project
	var operations []state.ProjectEnvironmentCloneOperation
	for _, slug := range []string{"locked", "available"} {
		p, err := s.CreateProject(ctx, state.Project{AccountID: a.ID, Slug: slug})
		if err != nil {
			t.Fatal(err)
		}
		op, err := s.CreateProjectEnvironmentCloneOperation(ctx, state.ProjectEnvironmentCloneOperation{
			AccountID: a.ID, ProjectID: p.ID, SourceEnvironment: "production", TargetEnvironment: "stage",
			IdempotencyKey: slug, SourceRevisionHash: strings.Repeat("a", 64),
		})
		if err != nil {
			t.Fatal(err)
		}
		projects, operations = append(projects, p), append(operations, op)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, "SELECT id FROM projects WHERE id = $1 FOR UPDATE", projects[0].ID); err != nil {
		t.Fatal(err)
	}
	bounded, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	lease, err := s.ClaimNextProjectEnvironmentClone(bounded, uuid.NewString(), time.Minute)
	if err != nil || lease.Operation.ID != operations[1].ID {
		t.Fatalf("locked project blocked an independent operation: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	lease, err = s.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
	if err != nil || lease.Operation.ID != operations[0].ID {
		t.Fatalf("unlocked operation was not claimed: %v", err)
	}
}
