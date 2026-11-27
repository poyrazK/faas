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

func TestPgAPISandboxComparisonReservesBothLegsOrNeither(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	acct, err := store.CreateAccount(ctx, "sandbox-compare@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "comparison-source", RAMMB: 512})
	if err != nil {
		t.Fatal(err)
	}
	var releases [2]state.Deployment
	for i := range releases {
		source, err := store.CreateDeployment(ctx, state.Deployment{
			AppID: parent.ID, Kind: state.DeploymentKindTarball, Status: state.DeployLive,
			ReleaseCommand: []string{"python", "migrate.py"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetDeploymentRootfs(ctx, source.ID, "/apps/fixture.ext4", "/apps/fixture.ext4", 1048576); err != nil {
			t.Fatal(err)
		}
		releases[i] = source
	}
	older, newer := releases[0], releases[1]
	baselines, err := store.ListAPISandboxComparisonBaselines(ctx, newer)
	if err != nil || len(baselines) != 1 || baselines[0].DeploymentID != older.ID {
		t.Fatalf("expected only the earlier retained release, got %+v (%v)", baselines, err)
	}
	scenario, err := store.PutAPISandboxScenario(ctx, newer.ID, api.APISandboxScenarioInput{
		Seed: json.RawMessage(`{}`),
		APISandboxScenarioGuide: api.APISandboxScenarioGuide{
			Name: "Compare refunds", Requests: []api.APISandboxExampleRequest{{Title: "Read status", Method: "GET", Path: "/refunds", ExpectedStatus: 200}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().UTC().Add(time.Hour)
	baseline := state.App{
		AccountID: acct.ID, Slug: "api-sbx-pg-compare-a", RAMMB: 512, PreviewOfSlug: state.APISandboxPreviewParent(parent.Slug),
		PreviewPrState: state.PreviewPrStateOpen, PreviewExpiresAt: &expires, APISandboxRunID: uuid.NewString(),
		APISandboxScenario: &scenario,
		Manifest:           state.AppManifest{APISandboxSourceDeploymentID: older.ID, APISandboxRun: true},
	}
	candidate := baseline
	candidate.APISandboxRunID = uuid.NewString()
	candidate.Manifest.APISandboxSourceDeploymentID = newer.ID
	comparison := uuid.NewString()
	limits := api.MustLimitsFor(acct.Plan)
	if _, err := store.CreateAPISandboxComparison(ctx, comparison, baseline, candidate, limits); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("expected a conflict on the second sandbox: %v", err)
	}
	if _, err := store.GetAPISandboxRun(ctx, baseline.APISandboxRunID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("first leg survived a failed comparison admission: %v", err)
	}
	if _, err := store.GetAPISandboxComparison(ctx, comparison); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("comparison survived failed admission: %v", err)
	}

	candidate.Slug = "api-sbx-pg-compare-b"
	apps, err := store.CreateAPISandboxComparison(ctx, comparison, baseline, candidate, limits)
	if err != nil || apps[0].ID == "" || apps[0].ID == apps[1].ID {
		t.Fatalf("comparison pair was not reserved: %v", err)
	}
	got, err := store.GetAPISandboxComparison(ctx, comparison)
	if err != nil || got.Verdict != api.APISandboxComparisonRunning || got.Baseline.ID != baseline.APISandboxRunID ||
		got.Candidate.ID != candidate.APISandboxRunID || got.BaselineDeploymentID != older.ID || got.CandidateDeploymentID != newer.ID {
		t.Fatalf("bad comparison binding: %+v (%v)", got, err)
	}
	history, err := store.ListAPISandboxComparisons(ctx, newer.ID)
	if err != nil || len(history) != 1 || history[0].ID != comparison {
		t.Fatalf("candidate history lost the comparison: %+v (%v)", history, err)
	}
	history, err = store.ListAPISandboxComparisons(ctx, older.ID)
	if err != nil || len(history) != 0 {
		t.Fatalf("baseline incorrectly listed comparisons as candidate: %+v (%v)", history, err)
	}
}
