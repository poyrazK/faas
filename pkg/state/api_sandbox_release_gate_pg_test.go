package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgAPISandboxReleaseAdmissionRequiresReceipt(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	accountID, appID, _ := seedLiveDeploy(t, store, ctx, "sandbox-release-admission")
	required := true
	_, err := store.SetReleaseApprovalPolicy(ctx, state.ReleaseApprovalPolicy{
		AppID: appID, AccountID: accountID, RequiredApprovals: 1, AfterStage: 1, TimeoutMinutes: 60,
		Approvers: state.ReleaseApproversAdmin, RequirePassingSandboxSuite: &required, UpdatedBy: "reviewer",
	}, time.Now().Add(-time.Second))
	if err != nil {
		t.Fatal(err)
	}
	makeRelease := func(scope string) string {
		t.Helper()
		d, err := store.CreateDeployment(ctx, state.Deployment{
			AppID: appID, Scope: scope, Kind: state.DeploymentKindImage, ImageDigest: "sha256:candidate",
			ReleaseCommand: []string{"migrate"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetDeploymentRootfs(ctx, d.ID, "/apps/held.ext4", "held.ext4", 1024); err != nil {
			t.Fatal(err)
		}
		if err := store.UpdateDeploymentStatus(ctx, d.ID, state.DeploySnapshotting, ""); err != nil {
			t.Fatal(err)
		}
		return d.ID
	}
	candidate := makeRelease("default")
	params := state.CreateAppTaskParams{
		AccountID: accountID, AppID: appID, DeploymentID: candidate,
		Kind: state.AppTaskKindRelease, Command: []string{"migrate"},
	}

	// The older release writer uses the normal task API without evidence.
	if _, err := store.CreateAppTask(ctx, params); !errors.Is(err, state.ErrPassingSandboxSuiteRequired) {
		t.Fatalf("unfenced release writer returned %v", err)
	}
	if _, err := store.ReleaseAppTaskByDeployment(ctx, candidate); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("release task without a receipt survived: %v", err)
	}

	// The fence applies at commit, allowing a task and its receipt atomically.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	taskID, runID := uuid.NewString(), uuid.NewString()
	if _, err := tx.Exec(ctx, `
		INSERT INTO app_tasks (id, account_id, app_id, deployment_id, kind, command, deployment_scope, artifact_key, image_digest, timeout_seconds)
		VALUES ($1, $2, $3, $4, 'release', ARRAY['migrate'], 'default', 'held.ext4', 'sha256:candidate', 60)`,
		taskID, accountID, appID, candidate); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO api_sandbox_release_admissions (deployment_id, task_id, suite_run_id) VALUES ($1, $2, $3)`,
		candidate, taskID, runID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if gate, err := store.APISandboxReleaseGate(ctx, appID, candidate, false); err != nil || gate == nil || gate.Status != "admitted" || gate.SuiteRunID != runID {
		t.Fatalf("accepted release receipt = %+v, %v", gate, err)
	}

	params.DeploymentID = makeRelease("staging")
	if _, err := store.CreateAppTask(ctx, params); err != nil {
		t.Fatalf("non-production release was held by the production suite policy: %v", err)
	}
}
