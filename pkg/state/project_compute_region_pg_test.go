//go:build !no_pg

package state_test

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgProjectComputeRegionPersistenceAndGuards(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	acct, err := store.CreateAccount(t.Context(), "regional-project@example.test", "pro")
	if err != nil {
		t.Fatal(err)
	}
	region := state.ComputeRegion{ID: "eu-project", DisplayName: "Project", AcceptingNewApps: true}
	if _, err := store.UpsertComputeRegion(t.Context(), region); err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(t.Context(), state.Project{AccountID: acct.ID, Slug: "regional-project", ComputeRegion: region.ID, ScanSource: state.ProjectScanSourceCompose, ProductionBranch: "main"})
	if err != nil || project.ComputeRegion != region.ID {
		t.Fatalf("project=%+v err=%v", project, err)
	}
	for _, read := range []func() (state.Project, error){
		func() (state.Project, error) { return store.ProjectByID(t.Context(), project.ID) },
		func() (state.Project, error) { return store.ProjectBySlug(t.Context(), acct.ID, project.Slug) },
		func() (state.Project, error) {
			return store.SetProjectScanSource(t.Context(), project.ID, state.ProjectScanSourceCompose)
		},
	} {
		got, err := read()
		if err != nil || got.ComputeRegion != region.ID {
			t.Fatalf("read=%+v err=%v", got, err)
		}
	}
	list, err := store.ListProjectsForAccount(t.Context(), acct.ID)
	if err != nil || len(list) != 1 || list[0].ComputeRegion != region.ID {
		t.Fatalf("list=%+v err=%v", list, err)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE projects SET compute_region = '' WHERE id = $1", project.ID); err == nil {
		t.Fatal("direct SQL repinned project")
	}
	if _, err := pool.Exec(t.Context(), "DELETE FROM compute_regions WHERE id = $1", region.ID); err == nil {
		t.Fatal("deleted a region used by a project")
	}
	region.AcceptingNewApps = false
	if _, err := store.UpsertComputeRegion(t.Context(), region); err != nil {
		t.Fatal(err)
	}
	_, err = store.CreateProject(t.Context(), state.Project{AccountID: acct.ID, Slug: "paused-project", ComputeRegion: region.ID})
	if p := api.AsProblem(err); p == nil || p.Code != api.CodeComputeRegionAdmissionPaused {
		t.Fatalf("paused creation=%v", err)
	}
	if _, err := pool.Exec(t.Context(), "INSERT INTO projects (account_id, slug, compute_region) VALUES ($1, 'sql-project', $2)", acct.ID, region.ID); err == nil {
		t.Fatal("direct SQL bypassed paused admission")
	}
	_, err = store.CreateProject(t.Context(), state.Project{AccountID: acct.ID, Slug: "invalid-project", ComputeRegion: "EU-fsn"})
	if !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("invalid creation=%v", err)
	}
}
