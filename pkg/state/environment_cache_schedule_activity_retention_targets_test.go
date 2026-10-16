package state_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCacheScheduleActivityRetentionTargetBoundsMemory(t *testing.T) {
	testActivityRetentionTargetBounds(t, state.NewMemStore())
}
func TestCacheScheduleActivityRetentionTargetBoundsPostgres(t *testing.T) {
	testActivityRetentionTargetBounds(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}
func testActivityRetentionTargetBounds(t *testing.T, s state.Store) {
	base := newActivityFixture(t, s)
	targets := make([]activityFixture, 0, api.EnvironmentCacheActivityRetentionTargets+1)
	for i := 0; i < api.EnvironmentCacheActivityRetentionTargets+1; i++ {
		f := *base
		var err error
		f.App, err = s.CreateApp(t.Context(), state.App{AccountID: f.Account.ID, ProjectID: f.Project.ID, Slug: fmt.Sprintf("retention-%d", i), WorkloadName: fmt.Sprintf("retention-%d", i), Type: state.AppTypeApp, RAMMB: 256})
		if err != nil {
			t.Fatal(err)
		}
		f.create(t, f.proposal(time.Now().Add(time.Hour)))
		targets = append(targets, f)
	}
	cutoff := time.Now().Add(time.Minute)
	pruner := s.(state.EnvironmentCacheScheduleActivityPruner)
	if err := pruner.PruneEnvironmentCacheScheduleActivity(t.Context(), cutoff); err != nil {
		t.Fatal(err)
	}
	expiredCount := 0
	for i, f := range targets {
		_, err := s.(state.EnvironmentCacheScheduleActivityReader).GetEnvironmentCacheScheduleActivity(t.Context(), f.Account.ID, f.App.ID, "staging", "", 1)
		var expired *state.EnvironmentCacheActivityExpiredError
		if errors.As(err, &expired) {
			expiredCount++
		} else if err != nil {
			t.Fatal(err)
		}
		if i == len(targets)-1 && err != nil {
			t.Fatal("oldest-first cleanup skipped an older target", err)
		}
	}
	if expiredCount != api.EnvironmentCacheActivityRetentionTargets {
		t.Fatal("unbounded target cleanup", expiredCount)
	}
	if err := pruner.PruneEnvironmentCacheScheduleActivity(t.Context(), cutoff); err != nil {
		t.Fatal(err)
	}
	last := targets[len(targets)-1]
	_, err := s.(state.EnvironmentCacheScheduleActivityReader).GetEnvironmentCacheScheduleActivity(t.Context(), last.Account.ID, last.App.ID, "staging", "", 1)
	var expired *state.EnvironmentCacheActivityExpiredError
	if !errors.As(err, &expired) {
		t.Fatal("cleanup did not drain backlog", err)
	}
}

func TestCacheScheduleActivityRetentionPreservesRecentMemory(t *testing.T) {
	s := state.NewMemStore()
	f := newActivityFixture(t, s)
	f.create(t, f.proposal(time.Now().Add(time.Hour)))
	cutoff := time.Now()
	f.create(t, f.proposal(time.Now().Add(time.Hour)))
	if err := s.PruneEnvironmentCacheScheduleActivity(t.Context(), cutoff); err != nil {
		t.Fatal(err)
	}
	_, err := s.GetEnvironmentCacheScheduleActivity(t.Context(), f.Account.ID, f.App.ID, "staging", "", 1)
	var expired *state.EnvironmentCacheActivityExpiredError
	if !errors.As(err, &expired) {
		t.Fatal(err)
	}
	page := f.events(t, expired.Recovery.OldestCursor, 50)
	if len(page.Entries) != 1 || page.Entries[0].Sequence != 2 {
		t.Fatal("recent activity was pruned", page)
	}
}
