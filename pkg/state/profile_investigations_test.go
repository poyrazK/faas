package state_test

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestSavedProfileInvestigationsPersistenceAndConcurrency(t *testing.T) {
	t.Setenv(pgtest.UseTemplateDatabase, "1")
	for _, backend := range []string{"mem", "pg"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			if backend == "pg" {
				pool := pgtest.OpenMigrated(t)
				if err := db.MigrateUp(t.Context(), pool); err != nil {
					t.Fatal(err)
				}
				store = state.NewPgStore(pool)
			}
			acct, err := store.CreateAccount(t.Context(), "profile-investigation@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "profile-investigation", Status: state.AppActive})
			if err != nil {
				t.Fatal(err)
			}
			dep, err := store.CreateDeployment(t.Context(), state.Deployment{ID: uuid.NewString(), AppID: app.ID, Scope: "prod", Kind: state.DeploymentKindTarball})
			if err != nil {
				t.Fatal(err)
			}
			other, err := store.CreateAccount(t.Context(), "profile-investigation-other@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			otherApp, err := store.CreateApp(t.Context(), state.App{AccountID: other.ID, Slug: "profile-other", Status: state.AppActive})
			if err != nil {
				t.Fatal(err)
			}
			otherDep, err := store.CreateDeployment(t.Context(), state.Deployment{ID: uuid.NewString(), AppID: otherApp.ID, Scope: "prod", Kind: state.DeploymentKindTarball})
			if err != nil {
				t.Fatal(err)
			}
			savedStore := store.(state.ProfileInvestigationStore)
			zero := int64(0)
			now := time.Now().UTC().Truncate(time.Microsecond)
			query := api.ProfileQuery{DeploymentID: dep.ID, Runtime: "node24", Start: now.Add(-time.Hour), End: now}
			req := api.SaveProfileInvestigationRequest{ExpectedRevision: &zero, Investigation: api.ProfileInvestigationInput{Title: "Deployment CPU regression", Notes: "Initial notes", Findings: "parseJSON became expensive", Baseline: query, Candidate: query, SelectedPath: &api.ProfileCallPath{View: "comparison", Frames: []api.ProfileCallPathFrame{{Name: "all"}, {Name: "parseJSON", File: "app.js", Line: 42}}}}}
			saved, err := savedStore.SaveProfileInvestigation(t.Context(), acct.ID, app.ID, "", req)
			if err != nil {
				t.Fatal(err)
			}
			if saved.Revision != 1 || saved.ID == "" || saved.AppID != app.ID {
				t.Fatal("missing saved identity", saved)
			}
			saved.Investigation.SelectedPath.Frames[1].Name = "caller mutated"
			got, err := savedStore.GetProfileInvestigation(t.Context(), acct.ID, app.ID, saved.ID)
			if err != nil || got.Investigation.SelectedPath.Frames[1].Name != "parseJSON" || !got.Investigation.Candidate.Start.Equal(query.Start) {
				t.Fatalf("persistence or aliasing: %+v %v", got, err)
			}
			if _, err = savedStore.GetProfileInvestigation(t.Context(), other.ID, app.ID, saved.ID); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("foreign read", err)
			}
			if _, err = savedStore.ListProfileInvestigations(t.Context(), other.ID, app.ID); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("foreign list", err)
			}
			if _, err = savedStore.SaveProfileInvestigation(t.Context(), other.ID, app.ID, "", req); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("foreign create", err)
			}
			if err = savedStore.DeleteProfileInvestigation(t.Context(), other.ID, app.ID, saved.ID, 1); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("foreign delete", err)
			}
			foreign := req
			foreign.Investigation.Candidate.DeploymentID = otherDep.ID
			if _, err = savedStore.SaveProfileInvestigation(t.Context(), acct.ID, app.ID, "", foreign); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("foreign deployment", err)
			}
			if _, err = savedStore.SaveProfileInvestigation(t.Context(), acct.ID, app.ID, saved.ID, req); !errors.Is(err, state.ErrProfileInvestigationRevision) {
				t.Fatal("stale revision", err)
			}
			results := make(chan error, 2)
			var writers sync.WaitGroup
			for _, notes := range []string{"Team member A", "Team member B"} {
				writers.Go(func() {
					update := req
					update.ExpectedRevision = &got.Revision
					update.Investigation.Notes = notes
					_, err := savedStore.SaveProfileInvestigation(t.Context(), acct.ID, app.ID, saved.ID, update)
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
				t.Fatal("concurrent updates lost", wins, conflicts)
			}
			rows, err := savedStore.ListProfileInvestigations(t.Context(), acct.ID, app.ID)
			if err != nil || len(rows) != 1 || rows[0].Revision != 2 {
				t.Fatal("list", rows, err)
			}
			// Race creation at the final available slot: the app lock guards quota.
			for range api.ProfileInvestigationMaxPerApp - 2 {
				if _, err := savedStore.SaveProfileInvestigation(t.Context(), acct.ID, app.ID, "", req); err != nil {
					t.Fatal(err)
				}
			}
			quotaResults := make(chan error, 2)
			for range 2 {
				writers.Go(func() {
					_, err := savedStore.SaveProfileInvestigation(t.Context(), acct.ID, app.ID, "", req)
					quotaResults <- err
				})
			}
			writers.Wait()
			close(quotaResults)
			wins, conflicts = 0, 0
			for err := range quotaResults {
				if err == nil {
					wins++
				} else if errors.Is(err, state.ErrProfileInvestigationQuota) {
					conflicts++
				} else {
					t.Fatal(err)
				}
			}
			if wins != 1 || conflicts != 1 {
				t.Fatal("concurrent quota overrun", wins, conflicts)
			}
			if err := savedStore.DeleteProfileInvestigation(t.Context(), acct.ID, app.ID, saved.ID, 1); !errors.Is(err, state.ErrProfileInvestigationRevision) {
				t.Fatal("stale delete", err)
			}
			if err := savedStore.DeleteProfileInvestigation(t.Context(), acct.ID, app.ID, saved.ID, 2); err != nil {
				t.Fatal(err)
			}
			if _, err := savedStore.GetProfileInvestigation(t.Context(), acct.ID, app.ID, saved.ID); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("deleted investigation", err)
			}
			if err := store.DeleteApp(t.Context(), app.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := savedStore.ListProfileInvestigations(t.Context(), acct.ID, app.ID); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("deleted app exposed investigations", err)
			}
		})
	}
}

func TestSavedProfileInvestigationValidation(t *testing.T) {
	zero := int64(0)
	now := time.Now().UTC()
	q := api.ProfileQuery{DeploymentID: "11111111-1111-4111-8111-111111111111", Runtime: "node24", Start: now.Add(-time.Hour), End: now}
	req := api.SaveProfileInvestigationRequest{ExpectedRevision: &zero, Investigation: api.ProfileInvestigationInput{Title: "Performance regression", Baseline: q, Candidate: q}}
	for _, tc := range []struct {
		name   string
		change func(*api.SaveProfileInvestigationRequest)
	}{
		{"missing revision", func(r *api.SaveProfileInvestigationRequest) { r.ExpectedRevision = nil }},
		{"empty title", func(r *api.SaveProfileInvestigationRequest) { r.Investigation.Title = "  " }},
		{"UTF8 byte limit", func(r *api.SaveProfileInvestigationRequest) {
			r.Investigation.Title = strings.Repeat("é", api.ProfileInvestigationMaxTitleBytes)
		}},
		{"notes limit", func(r *api.SaveProfileInvestigationRequest) {
			r.Investigation.Notes = strings.Repeat("n", api.ProfileInvestigationMaxTextBytes+1)
		}},
		{"null", func(r *api.SaveProfileInvestigationRequest) { r.Investigation.Findings = "a\x00b" }},
		{"runtime mismatch", func(r *api.SaveProfileInvestigationRequest) { r.Investigation.Candidate.Runtime = "python313" }},
		{"future selection", func(r *api.SaveProfileInvestigationRequest) { r.Investigation.Candidate.End = now.Add(time.Hour) }},
		{"path depth", func(r *api.SaveProfileInvestigationRequest) {
			r.Investigation.SelectedPath = &api.ProfileCallPath{View: "comparison", Frames: make([]api.ProfileCallPathFrame, api.ProfileMaxStackDepth+2)}
		}},
		{"path byte budget", func(r *api.SaveProfileInvestigationRequest) {
			frames := make([]api.ProfileCallPathFrame, 5)
			for i := range frames {
				frames[i].Name = strings.Repeat("a", api.ProfileMaxSymbolBytes)
			}
			r.Investigation.SelectedPath = &api.ProfileCallPath{View: "comparison", Frames: frames}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := req
			tc.change(&bad)
			if err := state.ValidateProfileInvestigation(bad); err == nil {
				t.Fatal("invalid investigation accepted")
			}
		})
	}
	if err := state.ValidateProfileInvestigation(req); err != nil {
		t.Fatal(err)
	}
}
