package state_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runenv"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemSystemPackageEnvironmentUsesVersionedWorkerLane(t *testing.T) {
	exerciseSystemEnvironmentLane(t, state.NewMemStore())
}

func exerciseSystemEnvironmentLane(t *testing.T, store environmentContractStore) {
	t.Helper()
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	row := environmentFixture(account.ID)
	row.Recipe = api.CreateExecutionEnvironmentRequest{Runtime: row.Runtime, SystemPackages: []string{"poppler-utils=26.10.0-r0"}}
	row.RecipeDigest, err = runenv.Digest(row.Recipe, row.BaseRef)
	if err != nil {
		t.Fatal(err)
	}
	row, err = store.CreateExecutionEnvironment(ctx, row)
	if err != nil || row.Status != "queued_system_v1" {
		t.Fatal(row, err)
	}
	// The N-1 worker's predicates recognize queued/building/built/imaging only.
	// The new lane must stay versioned through both owner handoffs.
	build, err := store.ClaimExecutionEnvironment(ctx, "build")
	if err != nil || build.Status != "building_system_v1" {
		t.Fatal(build, err)
	}
	if diagnostics, ok := store.(state.ExecutionEnvironmentDiagnosticStore); ok {
		if err := diagnostics.AppendExecutionEnvironmentEvent(ctx, build, api.ExecutionEnvironmentEvent{Stage: "build", Message: "Installing pinned system packages."}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.PublishExecutionEnvironmentBuild(ctx, build, strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}
	built, err := store.ExecutionEnvironmentByID(ctx, account.ID, row.ID)
	if err != nil || built.Status != "built_system_v1" {
		t.Fatal(built, err)
	}
	image, err := store.ClaimExecutionEnvironment(ctx, "image")
	if err != nil || image.Status != "imaging_system_v1" {
		t.Fatal(image, err)
	}
	if err := store.PublishExecutionEnvironmentImage(ctx, image, strings.Repeat("c", 64), strings.Repeat("d", 64), 1<<20); err != nil {
		t.Fatal(err)
	}
	ready, err := store.ExecutionEnvironmentByID(ctx, account.ID, row.ID)
	if err != nil || ready.Status != "ready" {
		t.Fatal(ready, err)
	}
	if runenv.PublicStatus(build.Status) != "building" {
		t.Fatal("worker version escaped public contract")
	}
	// Mutating a returned recipe cannot alter the retained environment intent.
	ready.Recipe.SystemPackages[0] = "other=1.0-r0"
	again, err := store.ExecutionEnvironmentByID(ctx, account.ID, row.ID)
	if err != nil || again.Recipe.SystemPackages[0] != "poppler-utils=26.10.0-r0" {
		t.Fatal(again, err)
	}
}
