package state_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgAPISandboxApprovalCheckClonesOnlyItsPinnedHeldArtifact(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	account, err := store.CreateAccount(ctx, "sandbox-review@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "sandbox-approval-source", RAMMB: 512})
	if err != nil {
		t.Fatal(err)
	}
	makeRelease := func(status state.DeploymentStatus, key string) state.Deployment {
		t.Helper()
		d, err := store.CreateDeployment(ctx, state.Deployment{
			AppID: parent.ID, Kind: state.DeploymentKindTarball, Status: status, ReleaseCommand: []string{"python", "migrate.py"},
			OverrideEnv: json.RawMessage(`{"PRODUCTION_ONLY":"private"}`),
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetDeploymentRootfs(ctx, d.ID, key, key, 1048576); err != nil {
			t.Fatal(err)
		}
		if err := store.UpdateDeploymentStatus(ctx, d.ID, status, ""); err != nil {
			t.Fatal(err)
		}
		d, err = store.DeploymentByID(ctx, d.ID)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	baseline := makeRelease(state.DeployLive, "/apps/sandbox-approval-baseline.ext4")
	candidate := makeRelease(state.DeploySnapshotting, "/apps/sandbox-approval-held.ext4")
	scenario, err := store.PutAPISandboxScenario(ctx, baseline.ID, api.APISandboxScenarioInput{
		Seed: json.RawMessage(`{}`),
		APISandboxScenarioGuide: api.APISandboxScenarioGuide{
			Name:     "Approval fixture",
			Requests: []api.APISandboxExampleRequest{{Title: "Read fixture", Method: "GET", Path: "/fixture", ExpectedStatus: 200}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().UTC().Add(time.Hour)
	buildSandbox := func(sourceID, slug string) state.App {
		return state.App{
			AccountID: account.ID, Slug: slug, RAMMB: 512, PreviewOfSlug: state.APISandboxPreviewParent(parent.Slug),
			PreviewPrState: state.PreviewPrStateOpen, PreviewExpiresAt: &expires, APISandboxRunID: uuid.NewString(),
			APISandboxScenario: &scenario,
			Manifest:           state.AppManifest{APISandboxSourceDeploymentID: sourceID, APISandboxRun: true},
		}
	}
	limits := api.MustLimitsFor(account.Plan)
	ordinary, err := store.CreateAppIfUnderQuota(ctx, buildSandbox(candidate.ID, "api-sbx-pg-unapproved"), limits)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPISandboxDeployment(ctx, account.ID, ordinary.ID, candidate.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("ordinary sandbox cloned a held release: %v", err)
	}

	pins := state.APISandboxApprovalPins{Baseline: state.APISandboxArtifactPin(baseline), Candidate: state.APISandboxArtifactPin(candidate)}
	id := uuid.NewString()
	apps, err := store.CreateAPISandboxApprovalCheck(ctx, id,
		buildSandbox(baseline.ID, "api-sbx-pg-approval-a"), buildSandbox(candidate.ID, "api-sbx-pg-approval-b"), limits, pins)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := store.GetAPISandboxApprovalCheck(ctx, id)
	if err != nil || stored.Pins != pins || stored.Comparison.BaselineDeploymentID != baseline.ID || stored.Comparison.CandidateDeploymentID != candidate.ID {
		t.Fatalf("did not preserve evidence pins: %+v (%v)", stored, err)
	}
	if _, err := store.GetAPISandboxComparison(ctx, id); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("approval receipts escaped into the unpinned comparison path: %v", err)
	}
	if err := store.SetDeploymentRootfs(ctx, candidate.ID, "/apps/replacement.ext4", "/apps/replacement.ext4", 1048576); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPISandboxDeployment(ctx, account.ID, apps[1].ID, candidate.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("changed candidate artifact was cloned: %v", err)
	}
	if err := store.SetDeploymentRootfs(ctx, candidate.ID, candidate.RootfsPath, candidate.RootfsKey, candidate.RootfsBytes); err != nil {
		t.Fatal(err)
	}
	clone, err := store.CreateAPISandboxDeployment(ctx, account.ID, apps[1].ID, candidate.ID)
	if err != nil || clone.AppID != apps[1].ID || clone.RootfsKey != candidate.RootfsKey || len(clone.ReleaseCommand) == 0 ||
		string(clone.OverrideEnv) != "" && string(clone.OverrideEnv) != "{}" && string(clone.OverrideEnv) != "null" {
		t.Fatalf("held sandbox did not clone isolated inputs: %v", err)
	}
	after, err := store.DeploymentByID(ctx, candidate.ID)
	if err != nil || after.Status != state.DeploySnapshotting {
		t.Fatalf("test clone advanced production candidate: %v", err)
	}
}
