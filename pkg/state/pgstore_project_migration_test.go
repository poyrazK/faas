//go:build !no_pg

package state_test

import (
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPgProjectMigrationRestartReplayAndProjectLease(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, _, _ := seedLiveDeploy(t, s, ctx, "project-migration")
	now := time.Now().UTC()
	r := state.ProjectMigrationRecord{AccountID: account, RequestDigest: "private-hmac", Report: api.ProjectMigration{ID: uuid.NewString(), Project: "shop", SourceSHA256: "source-hash", Status: "prepared", CreatedAt: now, UpdatedAt: now, Services: []api.ProjectMigrationService{{Name: "api", Status: "pending", Attempt: 1, ConfiguredKeys: []string{"DATABASE_URL"}}}}}
	r.Report.Services = append(r.Report.Services, api.ProjectMigrationService{Name: "web", Status: "pending", Attempt: 1, ServiceConnections: map[string]string{"API_URL": "api"}, PublicServiceConnections: map[string]string{"VITE_API_URL": "api"}, PublicBuildURLs: map[string]string{"VITE_API_URL": "https://shop-api.example.com"}, BuildDependencies: []string{"api"}, ConfiguredKeys: []string{"API_URL", "VITE_API_URL"}})
	r, err := s.CreateProjectMigration(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetProjectMigration(ctx, uuid.NewString(), r.Report.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("foreign receipt readable")
	}
	changed := r
	changed.RequestDigest = "changed-hmac"
	if _, err = s.CreateProjectMigration(ctx, changed); !errors.Is(err, state.ErrConflict) {
		t.Fatal("changed replay accepted")
	}
	restarted := state.NewPgStore(pool)
	rows, err := restarted.ListProjectMigrations(ctx, account, "shop")
	if err != nil || len(rows) != 1 || rows[0].Report.Services[0].ConfiguredKeys[0] != "DATABASE_URL" {
		t.Fatalf("restart lost journal: %v", err)
	}
	if len(rows[0].Report.Services[1].BuildDependencies) != 1 || rows[0].Report.Services[1].BuildDependencies[0] != "api" {
		t.Fatal("restart lost build wait intent")
	}
	if rows[0].Report.Services[1].ServiceConnections["API_URL"] != "api" {
		t.Fatal("restart lost connection intent")
	}
	if rows[0].Report.Services[1].PublicServiceConnections["VITE_API_URL"] != "api" || rows[0].Report.Services[1].PublicBuildURLs["VITE_API_URL"] != "https://shop-api.example.com" {
		t.Fatal("restart lost reviewed public build configuration")
	}
	var won atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			claim := r
			claim.LeaseUntil = now.Add(time.Minute)
			claim.Report.Status = "applying"
			if _, e := restarted.SaveProjectMigration(ctx, claim, r.Version); e == nil {
				won.Add(1)
			} else if !errors.Is(e, state.ErrConflict) {
				t.Error(e)
			}
		})
	}
	wg.Wait()
	if won.Load() != 1 {
		t.Fatalf("claims=%d", won.Load())
	}
	another := r
	another.Report.ID = uuid.NewString()
	another, err = s.CreateProjectMigration(ctx, another)
	if err != nil {
		t.Fatal(err)
	}
	another.LeaseUntil = now.Add(time.Minute)
	if _, err = s.SaveProjectMigration(ctx, another, another.Version); !errors.Is(err, state.ErrConflict) {
		t.Fatal("simultaneous project apply accepted")
	}
	old, err := restarted.GetProjectMigration(ctx, account, r.Report.ID)
	if err != nil {
		t.Fatal(err)
	}
	old.LeaseUntil = time.Time{}
	old.Report.Status = "tracking"
	if _, err = restarted.SaveProjectMigration(ctx, old, old.Version); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveProjectMigration(ctx, another, another.Version); err != nil {
		t.Fatal("released project remained blocked", err)
	}
}

func TestPgMigrationBuildWaitSurvivesRestart(t *testing.T) {
	s, pool, _ := pgStoreWithPool(t)
	_, _, target, candidate, build := buildWaitFixture(t, "postgres", false, s)
	claim, err := s.ClaimQueuedBuild(t.Context(), build.ID)
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.CheckBuildDependencies(t.Context(), claim, time.Now().Add(-api.ProjectMigrationBuildWaitPoll-time.Second))
	if err != nil {
		t.Fatal(err)
	}
	restarted := state.NewPgStore(pool)
	claim, err = restarted.ClaimQueuedBuild(t.Context(), build.ID)
	if err != nil {
		t.Fatal(err)
	}
	after, err := restarted.CheckBuildDependencies(t.Context(), claim, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !before.DeadlineAt.Equal(after.DeadlineAt) || after.Status != "waiting" || after.Dependencies[0].DeploymentID != target.ID {
		t.Fatalf("restart lost waiting inputs: %+v", after)
	}
	durable, err := restarted.DeploymentByID(t.Context(), candidate.ID)
	if err != nil || len(durable.StageState) == 0 {
		t.Fatal("restart lost stage projection", err)
	}
}
