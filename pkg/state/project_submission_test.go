package state_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProjectSubmissionMemory(t *testing.T) { testProjectSubmission(t, state.NewMemStore()) }
func testProjectSubmission(t *testing.T, store state.Store) {
	account, err := store.CreateAccount(t.Context(), "project-receipt@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "receipt"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateAccount(t.Context(), "other-receipt@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	ledger := store.(state.ProjectSubmissionStore)
	id := strings.Repeat("a", 32)
	frame := api.ProjectSubmissionReceipt{AttemptID: id, ProjectSlug: "demo", Status: "processing", Apply: api.ApplyResponse{Builds: []api.AppliedBuild{{Slug: app.Slug, AppID: app.ID}}}}
	reserved, err := ledger.ReserveProjectSubmission(t.Context(), account.ID, id, strings.Repeat("b", 64), frame)
	if err != nil || !reserved {
		t.Fatalf("reserve=%v %v", reserved, err)
	}
	reserved, err = ledger.ReserveProjectSubmission(t.Context(), account.ID, id, strings.Repeat("b", 64), frame)
	if err != nil || reserved {
		t.Fatalf("duplicate=%v %v", reserved, err)
	}
	if _, err := ledger.ReserveProjectSubmission(t.Context(), account.ID, id, strings.Repeat("c", 64), frame); !errors.Is(err, state.ErrProjectSubmissionConflict) {
		t.Fatalf("changed request=%v", err)
	}
	if _, err := ledger.ProjectSubmission(t.Context(), other.ID, id); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("ownership=%v", err)
	}
	ctx := state.ContextWithDeploymentSubmission(t.Context(), state.ProjectWorkloadAttempt(id, app.ID))
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, TrafficPercent: 100})
	if err != nil {
		t.Fatal(err)
	}
	// No progress write follows creation: reconstruct acceptance from its atomic receipt.
	receipt, err := ledger.ProjectSubmission(t.Context(), account.ID, id)
	if err != nil || receipt.Apply.Builds[0].DeploymentID != dep.ID {
		t.Fatalf("atomic acceptance=%+v %v", receipt, err)
	}
	frame.Status = "complete"
	if err := ledger.WriteProjectSubmission(t.Context(), account.ID, frame); err != nil {
		t.Fatal(err)
	}
	if err := ledger.WriteProjectSubmission(t.Context(), account.ID, frame); !errors.Is(err, state.ErrProjectSubmissionConflict) {
		t.Fatalf("terminal receipt overwritten: %v", err)
	}
	receipt, err = ledger.ProjectSubmission(t.Context(), account.ID, id)
	if err != nil || receipt.Status != "complete" || receipt.Apply.Builds[0].DeploymentID != dep.ID {
		t.Fatalf("complete=%+v %v", receipt, err)
	}
}
