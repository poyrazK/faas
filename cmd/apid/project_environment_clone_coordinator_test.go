// adr: 583
package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCloneCoordinatorRejectsIncompleteConfigurationBeforePreparation(t *testing.T) {
	hash := strings.Repeat("a", 64)
	op := state.ProjectEnvironmentCloneOperation{ID: "operation", SourceEnvironment: "production", SourceRevisionHash: hash}
	capture := state.ProjectEnvironmentCloneConfigurationCapture{OperationID: op.ID, Version: 1, Hash: hash, WorkloadCount: 1}
	view := state.ProjectEnvironmentCloneWorkload{OperationID: op.ID, AppID: "app", WorkloadSlug: "api", SourceDeploymentID: "deployment",
		SourceHash: hash, SourceSettingsHash: hash, SourceValuesHash: hash, SourcePoliciesHash: hash, SourceProjectConfigHash: hash}
	op.Resources = []state.ProjectEnvironmentCloneResource{
		{Kind: "source_revision", Name: "production", SourceVersion: hash, Status: "ready"},
		{Kind: "project_config", Name: "production", SourceVersion: hash, Status: "captured"},
		{Kind: "workload", Name: "api", SourceID: "deployment", SourceVersion: hash, Status: "captured"},
		{Kind: "variables", Name: "api", SourceID: "app", TargetID: "app", SourceVersion: hash, Status: "captured"},
		{Kind: "secrets", Name: "api", SourceID: "app", TargetID: "app", SourceVersion: hash, Status: "captured"},
	}
	if err := validateCloneCoordinatorInventory(op, capture, []state.ProjectEnvironmentCloneWorkload{view}); err != nil {
		t.Fatal(err)
	}
	for _, fault := range []string{"legacy_capture", "wrong_root", "wrong_count", "missing_root", "missing_values", "duplicate", "unknown_kind", "wrong_artifact", "wrong_values", "wrong_target", "failed_resource", "operation_error", "wrong_owner"} {
		t.Run(fault, func(t *testing.T) {
			bad, root, workload := op, capture, view
			bad.Resources = append([]state.ProjectEnvironmentCloneResource(nil), op.Resources...)
			switch fault {
			case "legacy_capture":
				root.Version = 0
			case "wrong_root":
				root.Hash = strings.Repeat("b", 64)
			case "wrong_count":
				root.WorkloadCount++
			case "missing_root":
				bad.Resources = bad.Resources[1:]
			case "missing_values":
				bad.Resources = bad.Resources[:4]
			case "duplicate":
				bad.Resources = append(bad.Resources, bad.Resources[0])
			case "unknown_kind":
				bad.Resources = append(bad.Resources, state.ProjectEnvironmentCloneResource{Kind: "external_service", Name: "payments", Status: "captured"})
			case "wrong_artifact":
				bad.Resources[2].SourceVersion = strings.Repeat("b", 64)
			case "wrong_values":
				bad.Resources[3].SourceVersion = strings.Repeat("b", 64)
			case "wrong_target":
				bad.Resources[2].TargetID = "foreign-deployment"
			case "failed_resource":
				bad.Resources[3].Status = "failed"
			case "operation_error":
				bad.ErrorCode = "capture_failed"
			case "wrong_owner":
				workload.OperationID = "another-operation"
			}
			if err := validateCloneCoordinatorInventory(bad, root, []state.ProjectEnvironmentCloneWorkload{workload}); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("incomplete configuration accepted: %v", err)
			}
		})
	}
}

func TestCloneCopyingCheckpointCanWaitWithoutRebindingCapture(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "checkpoint@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "checkpoint"})
	if err != nil {
		t.Fatal(err)
	}
	op, err := store.CreateProjectEnvironmentCloneOperation(ctx, state.ProjectEnvironmentCloneOperation{AccountID: account.ID, ProjectID: project.ID,
		SourceEnvironment: "production", TargetEnvironment: "stage", IdempotencyKey: "checkpoint", SourceRevisionHash: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	op, err = store.AdvanceProjectEnvironmentCloneOperation(ctx, account.ID, project.ID, op.ID, op.Status, state.CloneOperationCapturing, op.Revision, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	resources := []state.ProjectEnvironmentCloneResource{{Kind: "source_revision", Name: "production", SourceVersion: op.SourceRevisionHash, Status: "ready"},
		{Kind: "workload", Name: "api", SourceID: "source-deployment", SourceVersion: strings.Repeat("b", 64), Status: "captured"}}
	for _, status := range []string{"copying", "verifying"} {
		bad := append([]state.ProjectEnvironmentCloneResource(nil), resources...)
		bad[1].Status = status
		if _, err := store.AdvanceProjectEnvironmentCloneOperation(ctx, account.ID, project.ID, op.ID, op.Status, state.CloneOperationCopying, op.Revision, bad, ""); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("entered copying with uncaptured %s resource: %v", status, err)
		}
	}
	op, err = store.AdvanceProjectEnvironmentCloneOperation(ctx, account.ID, project.ID, op.ID, op.Status, state.CloneOperationCopying, op.Revision, resources, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"copying", "verifying", "ready"} {
		resources[1].Status, resources[1].TargetID = status, "target-deployment"
		op, err = store.AdvanceProjectEnvironmentCloneOperation(ctx, account.ID, project.ID, op.ID, op.Status, op.Status, op.Revision, resources, "")
		if err != nil {
			t.Fatalf("durable %s checkpoint rejected: %v", status, err)
		}
	}
	for _, fault := range []string{"source", "version", "target", "additional_resource", "planned"} {
		bad := append([]state.ProjectEnvironmentCloneResource(nil), resources...)
		switch fault {
		case "source":
			bad[1].SourceID = "different-source"
		case "version":
			bad[1].SourceVersion = strings.Repeat("c", 64)
		case "target":
			bad[1].TargetID = "different-target"
		case "additional_resource":
			bad = append(bad, state.ProjectEnvironmentCloneResource{Kind: "variables", Name: "extra", Status: "captured"})
		case "planned":
			bad[1].Status = "planned"
		}
		if _, err := store.AdvanceProjectEnvironmentCloneOperation(ctx, account.ID, project.ID, op.ID, op.Status, op.Status, op.Revision, bad, ""); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("%s changed frozen capture: %v", fault, err)
		}
	}
}
