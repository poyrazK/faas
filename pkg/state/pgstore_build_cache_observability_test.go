package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgBuildCacheOutcomeAndStats(t *testing.T) {
	s, ctx := pgStore(t)
	_, appID, depID := seedLiveDeploy(t, s, ctx, "cache-pg")

	key := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	builds := []struct {
		status string
		key    string
	}{
		{status: "hit", key: key},
		{status: "miss"},
		{status: "invalidated"},
	}

	for i, want := range builds {
		build, err := s.CreateBuild(ctx, depID, state.DeploymentKindTarball, int64(i+1), "")
		if err != nil {
			t.Fatalf("CreateBuild[%d]: %v", i, err)
		}
		if i == 0 {
			if err := s.SetBuildCacheOutcome(ctx, build.ID, want.status, want.key); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("SetBuildCacheOutcome before claim = %v, want ErrNotFound", err)
			}
		}
		if _, err := s.ClaimQueuedBuild(ctx, build.ID); err != nil {
			t.Fatalf("ClaimQueuedBuild[%d]: %v", i, err)
		}
		if err := s.SetBuildCacheOutcome(ctx, build.ID, want.status, want.key); err != nil {
			t.Fatalf("SetBuildCacheOutcome[%d]: %v", i, err)
		}
		if err := s.UpdateBuildStatus(ctx, build.ID, state.BuildSucceeded, "", false, true); err != nil {
			t.Fatalf("UpdateBuildStatus[%d]: %v", i, err)
		}
	}

	stats, err := s.BuildCacheStatsForApp(ctx, appID, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("BuildCacheStatsForApp: %v", err)
	}
	if stats.Hits != 1 || stats.Eligible != 3 {
		t.Fatalf("stats = %+v, want one hit out of three eligible builds", stats)
	}

	if err := s.SetBuildCacheOutcome(ctx, "00000000-0000-0000-0000-000000000000", "hit", ""); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("SetBuildCacheOutcome for unknown build = %v, want ErrNotFound", err)
	}
	future, err := s.BuildCacheStatsForApp(ctx, appID, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("BuildCacheStatsForApp future: %v", err)
	}
	if future != (state.BuildCacheStats{}) {
		t.Fatalf("future stats = %+v, want zero", future)
	}
}
