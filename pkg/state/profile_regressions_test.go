package state_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/profiling"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProfileRegressionPersistenceAndConcurrentChecks(t *testing.T) {
	t.Setenv(pgtest.UseTemplateDatabase, "1")
	for _, backend := range []string{"mem", "pg"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			if backend == "pg" {
				store = state.NewPgStore(pgtest.OpenMigrated(t))
			}
			acct, err := store.CreateAccount(t.Context(), "regression@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "regression", Status: state.AppActive})
			if err != nil {
				t.Fatal(err)
			}
			dep, err := store.CreateDeployment(t.Context(), state.Deployment{ID: uuid.NewString(), AppID: app.ID, Kind: state.DeploymentKindTarball})
			if err != nil {
				t.Fatal(err)
			}
			q := api.ProfileQuery{DeploymentID: dep.ID, Runtime: "node24", Start: time.Now().Add(-time.Hour), End: time.Now()}
			zero := int64(0)
			savedStore := store.(state.ProfileInvestigationStore)
			req := api.SaveProfileInvestigationRequest{ExpectedRevision: &zero, Investigation: api.ProfileInvestigationInput{Title: "Regression", Baseline: q, Candidate: q}}
			row, err := savedStore.SaveProfileInvestigation(t.Context(), acct.ID, app.ID, "", req)
			if err != nil {
				t.Fatal(err)
			}
			a := profiling.NewRegressionAssessment(row, api.DefaultProfileRegressionOptions(), time.Now())
			a.Reason = "Coverage unavailable"
			a.Evidence = []api.ProfileRegressionEvidence{{Kind: "call_path", Frames: []api.ProfileCallPathFrame{{Name: "work"}}}}
			if _, err := savedStore.SaveProfileRegressionAssessment(t.Context(), uuid.NewString(), app.ID, row.ID, 1, a); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("cross-account assessment", err)
			}
			bad := a
			bad.Candidate.DeploymentID = uuid.NewString()
			if _, err := savedStore.SaveProfileRegressionAssessment(t.Context(), acct.ID, app.ID, row.ID, 1, bad); err == nil {
				t.Fatal("wrong windows accepted")
			}
			results := make(chan error, 2)
			var writers sync.WaitGroup
			for range 2 {
				writers.Go(func() {
					_, err := savedStore.SaveProfileRegressionAssessment(t.Context(), acct.ID, app.ID, row.ID, 1, a)
					results <- err
				})
			}
			writers.Wait()
			close(results)
			wins, conflicts := 0, 0
			for err := range results {
				if err == nil {
					wins++
				} else if errors.Is(err, state.ErrProfileInvestigationRevision) {
					conflicts++
				} else {
					t.Fatal(err)
				}
			}
			if wins != 1 || conflicts != 1 {
				t.Fatal("checks overwrote each other", wins, conflicts)
			}
			a.Evidence[0].Frames[0].Name = "mutated"
			got, err := savedStore.GetProfileInvestigation(t.Context(), acct.ID, app.ID, row.ID)
			if err != nil || got.Revision != 2 || got.Assessment == nil || got.Assessment.InvestigationRevision != 2 || got.Assessment.Evidence[0].Frames[0].Name != "work" {
				t.Fatal("assessment persistence/aliasing", got, err)
			}
			got.Assessment.Evidence[0].Frames[0].Name = "reader mutated"
			req.ExpectedRevision = &got.Revision
			req.Investigation.Notes = "New finding"
			updated, err := savedStore.SaveProfileInvestigation(t.Context(), acct.ID, app.ID, row.ID, req)
			if err != nil || updated.Revision != 3 || updated.Assessment == nil || updated.Assessment.InvestigationRevision != 2 || updated.Assessment.Evidence[0].Frames[0].Name != "work" {
				t.Fatal("historical result lost or not stale", updated, err)
			}
			if _, err := savedStore.SaveProfileRegressionAssessment(t.Context(), acct.ID, app.ID, row.ID, 2, a); !errors.Is(err, state.ErrProfileInvestigationRevision) {
				t.Fatal("edit during check was overwritten", err)
			}
		})
	}
}
