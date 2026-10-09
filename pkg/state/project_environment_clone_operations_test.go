package state

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemProjectEnvironmentCloneOperationPinsSourceAndFencesTransitions(t *testing.T) {
	ctx := context.Background()
	s := NewMemStore()
	acct, err := s.CreateAccount(ctx, "clone-operation@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.CreateProject(ctx, Project{AccountID: acct.ID, Slug: "shop", ScanSource: ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	input := ProjectEnvironmentCloneOperation{
		AccountID: acct.ID, ProjectID: project.ID, SourceEnvironment: "production", TargetEnvironment: "staging",
		IdempotencyKey: "clone-1", SourceRevisionHash: strings.Repeat("a", 64),
	}
	created, err := s.CreateProjectEnvironmentCloneOperation(ctx, input)
	if err != nil || created.ID == "" || created.Status != CloneOperationPending || created.Revision != 1 {
		t.Fatalf("create = %+v, %v", created, err)
	}
	if _, err := s.CreateProjectEnvironment(ctx, ProjectEnvironment{AccountID: acct.ID, ProjectID: project.ID, Slug: "staging"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("reserved target creation = %v, want conflict", err)
	}
	// A retry is recognized even after work on the target has started.
	replayed, err := s.CreateProjectEnvironmentCloneOperation(ctx, input)
	if err != nil || replayed.ID != created.ID {
		t.Fatalf("replay = %+v, %v", replayed, err)
	}
	changed := input
	changed.SourceRevisionHash = strings.Repeat("b", 64)
	if _, err := s.CreateProjectEnvironmentCloneOperation(ctx, changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed source revision = %v, want conflict", err)
	}
	if _, err := s.ProjectEnvironmentCloneOperationByID(ctx, "other-account", project.ID, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-account lookup = %v", err)
	}
	resource := ProjectEnvironmentCloneResource{Kind: "postgres", Name: "primary", SourceID: "source-id", CapturePoint: "lsn:123", TargetID: "target-id", Status: "captured"}
	advanced, err := s.AdvanceProjectEnvironmentCloneOperation(ctx, acct.ID, project.ID, created.ID, CloneOperationPending, CloneOperationCapturing, 1, []ProjectEnvironmentCloneResource{resource}, "")
	if err != nil || advanced.Status != CloneOperationCapturing || advanced.Revision != 2 || len(advanced.Resources) != 1 {
		t.Fatalf("advance = %+v, %v", advanced, err)
	}
	advanced.Resources[0].TargetID = "tampered"
	loaded, err := s.ProjectEnvironmentCloneOperationByIdempotencyKey(ctx, acct.ID, project.ID, input.IdempotencyKey)
	if err != nil || loaded.Resources[0].TargetID != "target-id" {
		t.Fatalf("stored checkpoint changed through returned slice: %+v, %v", loaded, err)
	}
	if _, err := s.AdvanceProjectEnvironmentCloneOperation(ctx, acct.ID, project.ID, created.ID, CloneOperationPending, CloneOperationCapturing, 1, nil, ""); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale status = %v, want conflict", err)
	}
	if _, err := s.AdvanceProjectEnvironmentCloneOperation(ctx, acct.ID, project.ID, created.ID, CloneOperationCapturing, CloneOperationCapturing, 1, nil, ""); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale revision = %v, want conflict", err)
	}
	if _, err := s.AdvanceProjectEnvironmentCloneOperation(ctx, acct.ID, project.ID, created.ID, CloneOperationCapturing, CloneOperationReady, 2, nil, ""); !errors.Is(err, ErrInvalidProjectEnvironmentCloneOperation) {
		t.Fatalf("invalid shortcut = %v", err)
	}
	if _, err := s.AdvanceProjectEnvironmentCloneOperation(ctx, acct.ID, project.ID, created.ID, CloneOperationCapturing, CloneOperationFailed, 2, []ProjectEnvironmentCloneResource{resource}, "capture_failed"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AdvanceProjectEnvironmentCloneOperation(ctx, acct.ID, project.ID, created.ID, CloneOperationFailed, CloneOperationCompensating, 3, []ProjectEnvironmentCloneResource{resource}, ""); err != nil {
		t.Fatal(err)
	}
	resource.Status = "compensated"
	if _, err := s.AdvanceProjectEnvironmentCloneOperation(ctx, acct.ID, project.ID, created.ID, CloneOperationCompensating, CloneOperationCompensated, 4, []ProjectEnvironmentCloneResource{resource}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AdvanceProjectEnvironmentCloneOperation(ctx, acct.ID, project.ID, created.ID, CloneOperationCompensated, CloneOperationReady, 5, nil, ""); !errors.Is(err, ErrInvalidProjectEnvironmentCloneOperation) {
		t.Fatalf("terminal transition = %v", err)
	}
}
