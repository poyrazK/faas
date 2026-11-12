package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemProjectEnvironmentBranchSlot(t *testing.T) {
	projectEnvironmentBranchSlot(t, state.NewMemStore())
}

// ADR-20261031193635 stable branch URLs: a branch's slot resolves to the
// environment of the branch holding it, and a branch deleted and made again
// under the same name holds the same slot with a new environment. Another
// project's branch of the same name has another slot.
func projectEnvironmentBranchSlot(t *testing.T, s contractTestStore) {
	t.Helper()
	ctx := context.Background()
	acct, err := s.CreateAccount(ctx, "branch-slot@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	limits := api.MustLimitsFor(acct.Plan)
	branchOf := func(t *testing.T, project state.Project, slug string) (state.ProjectEnvironment, state.ProjectEnvironmentBranch) {
		t.Helper()
		env, branch, _, err := s.CreateProjectEnvironmentBranch(ctx, state.ProjectEnvironmentBranchCreate{
			AccountID: acct.ID, ProjectID: project.ID, ParentSlug: "production", Slug: slug,
			ExpiresAt: time.Now().Add(time.Hour), EgressMode: state.EnvironmentBranchEgressLive, MaxDepth: api.EnvironmentBranchMaxDepth,
		}, limits)
		if err != nil {
			t.Fatal(err)
		}
		return env, branch
	}
	project, err := s.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "slotted"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "other"})
	if err != nil {
		t.Fatal(err)
	}

	env, branch := branchOf(t, project, "pr-1")
	if branch.SlotID == "" || branch.SlotID != state.EnvironmentBranchSlotID(project.ID, "pr-1") || branch.SlotID == env.ID {
		t.Fatalf("slot = %q (environment %s); want the slot of pr-1 in the project", branch.SlotID, env.ID)
	}
	if stored, err := s.ProjectEnvironmentBranchBySlug(ctx, acct.ID, project.ID, "pr-1"); err != nil || stored.SlotID != branch.SlotID {
		t.Fatalf("stored branch = %+v, %v; want slot %s", stored, err, branch.SlotID)
	}
	if resolved, err := s.ProjectEnvironmentByID(ctx, branch.SlotID); err != nil || resolved.ID != env.ID || resolved.Slug != "pr-1" {
		t.Fatalf("slot resolves to %+v, %v; want environment %s", resolved, err, env.ID)
	}
	if resolved, err := s.ProjectEnvironmentByID(ctx, env.ID); err != nil || resolved.ID != env.ID {
		t.Fatalf("environment ID resolves to %+v, %v", resolved, err)
	}

	// Reset: the branch is deleted and made again under the same name.
	if err := s.DeleteProjectEnvironmentBranch(ctx, acct.ID, project.ID, "pr-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProjectEnvironmentByID(ctx, branch.SlotID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("slot of a deleted branch: %v; want ErrNotFound", err)
	}
	again, againBranch := branchOf(t, project, "pr-1")
	if againBranch.SlotID != branch.SlotID || again.ID == env.ID {
		t.Fatalf("made again: slot %s environment %s; want slot %s with a new environment", againBranch.SlotID, again.ID, branch.SlotID)
	}
	if resolved, err := s.ProjectEnvironmentByID(ctx, branch.SlotID); err != nil || resolved.ID != again.ID {
		t.Fatalf("slot after the reset resolves to %+v, %v; want %s", resolved, err, again.ID)
	}

	_, otherBranch := branchOf(t, other, "pr-1")
	if otherBranch.SlotID == branch.SlotID {
		t.Fatalf("two projects' pr-1 share slot %s", branch.SlotID)
	}
}
