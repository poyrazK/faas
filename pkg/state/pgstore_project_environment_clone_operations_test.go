//go:build !no_pg

package state_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgProjectEnvironmentCloneOperationIdempotencyAndCAS(t *testing.T) {
	s, ctx, _ := pgWithPool(t)
	acct, err := s.CreateAccount(ctx, "clone-op-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "clone-op-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	input := state.ProjectEnvironmentCloneOperation{
		AccountID: acct.ID, ProjectID: project.ID, SourceEnvironment: "production", TargetEnvironment: "staging",
		IdempotencyKey: "clone-1", SourceRevisionHash: strings.Repeat("a", 64),
	}
	created, err := s.CreateProjectEnvironmentCloneOperation(ctx, input)
	if err != nil || created.Status != state.CloneOperationPending || created.Revision != 1 {
		t.Fatalf("create = %+v, %v", created, err)
	}
	if _, err := s.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: acct.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	if replayed, err := s.CreateProjectEnvironmentCloneOperation(ctx, input); err != nil || replayed.ID != created.ID {
		t.Fatalf("replay after target creation = %+v, %v", replayed, err)
	}
	changed := input
	changed.SourceRevisionHash = strings.Repeat("b", 64)
	if _, err := s.CreateProjectEnvironmentCloneOperation(ctx, changed); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("changed source = %v, want conflict", err)
	}
	resource := state.ProjectEnvironmentCloneResource{Kind: "object_storage", Name: "assets", SourceVersion: "generation:42", TargetID: "bucket-id", Status: "captured"}
	if _, err := s.AdvanceProjectEnvironmentCloneOperation(ctx, acct.ID, project.ID, created.ID, state.CloneOperationPending, state.CloneOperationCapturing, 1, []state.ProjectEnvironmentCloneResource{resource}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AdvanceProjectEnvironmentCloneOperation(ctx, acct.ID, project.ID, created.ID, state.CloneOperationPending, state.CloneOperationCapturing, 1, nil, ""); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale CAS = %v, want conflict", err)
	}
	if _, err := s.AdvanceProjectEnvironmentCloneOperation(ctx, acct.ID, project.ID, created.ID, state.CloneOperationCapturing, state.CloneOperationCapturing, 1, nil, ""); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale revision = %v, want conflict", err)
	}
	got, err := s.ProjectEnvironmentCloneOperationByID(ctx, acct.ID, project.ID, created.ID)
	if err != nil || got.Resources[0].SourceVersion != resource.SourceVersion {
		t.Fatalf("resource checkpoint = %+v, %v", got, err)
	}
	if _, err := s.ProjectEnvironmentCloneOperationByID(ctx, uuid.NewString(), project.ID, created.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account lookup = %v", err)
	}
}
