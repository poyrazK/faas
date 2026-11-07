//go:build !no_pg

// adr: 1445, 20261021152201 — bounded scans progress through stable timestamp/ID cursors.
package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestReleaseWorkerScanPagination(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			ctx := context.Background()
			var store state.Store = state.NewMemStore()
			if backend == "postgres" {
				store, ctx = pgStore(t)
			}
			schedules := store.(state.ScheduledReleaseStore)
			watch := store.(state.PostReleaseWatchStore)
			now := time.Now().UTC().Truncate(time.Microsecond)
			acct, err := store.CreateAccount(ctx, "release-pages@example.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 5; i++ {
				app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: fmt.Sprintf("release-page-%d", i), Type: state.AppTypeApp,
					RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
				if err != nil {
					t.Fatal(err)
				}
				create := func(createdAt time.Time, optedIn bool) state.Deployment {
					t.Helper()
					dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage,
						ImageDigest: "sha256:" + strings.Repeat("a", 64), Status: state.DeployPending, CreatedAt: createdAt, RollbackOn5xx: optedIn})
					if err != nil {
						t.Fatal(err)
					}
					return dep
				}
				previous := create(now.Add(-time.Hour), false)
				if err := store.MarkDeploymentLive(ctx, previous.ID); err != nil {
					t.Fatal(err)
				}
				current := create(now.Add(-10*time.Minute), true)
				if err := store.MarkDeploymentLive(ctx, current.ID); err != nil {
					t.Fatal(err)
				}
				if err := store.MarkDeploymentSuperseded(ctx, previous.ID); err != nil {
					t.Fatal(err)
				}
				// Three rows have identical timestamps, so pagination must use ID
				// as well. The last two rows lie after that tied group.
				completed := now.Add(-2*time.Minute + time.Duration(i/3)*time.Second)
				started := completed.Add(-4 * time.Minute)
				if _, err := store.SafedeployStampRollout(ctx, current.ID, "complete", &started, &completed, nil, ""); err != nil {
					t.Fatal(err)
				}
				pending := create(now, false)
				if _, _, err := schedules.ScheduleRelease(ctx, state.ScheduledRelease{AccountID: acct.ID, AppID: app.ID,
					DeploymentID: pending.ID, ReleaseAt: completed, ScheduledBy: "user:release-page"}, "", "", now); err != nil {
					t.Fatal(err)
				}
			}
			allDue, err := schedules.DueScheduledReleases(ctx, now, 10)
			if err != nil || len(allDue) != 5 {
				t.Fatalf("initial due schedules=%+v err=%v", allDue, err)
			}
			allWatched, err := watch.ListPostReleaseWatchCandidates(ctx, time.Hour, 10)
			if err != nil || len(allWatched) != 5 {
				t.Fatalf("initial watched releases=%+v err=%v", allWatched, err)
			}
			var dueCursor, watchCursor state.ReleaseScanCursor
			for start := 0; start < 5; start += 2 {
				count := min(2, 5-start)
				due, err := schedules.DueScheduledReleasesPage(ctx, now, 2, dueCursor)
				if err != nil || len(due) != count {
					t.Fatalf("schedule page %d=%+v err=%v", start, due, err)
				}
				watched, err := watch.ListPostReleaseWatchCandidatesPage(ctx, time.Hour, 2, watchCursor)
				if err != nil || len(watched) != count {
					t.Fatalf("watch page %d=%+v err=%v", start, watched, err)
				}
				for i := 0; i < count; i++ {
					if due[i].ID != allDue[start+i].ID || watched[i].DeploymentID != allWatched[start+i].DeploymentID {
						t.Fatalf("cursor skipped or repeated a row on page %d", start)
					}
				}
				lastDue, lastWatch := due[len(due)-1], watched[len(watched)-1]
				dueCursor = state.ReleaseScanCursor{At: lastDue.ReleaseAt, ID: lastDue.ID}
				watchCursor = state.ReleaseScanCursor{At: lastWatch.RolloutCompletedAt, ID: lastWatch.DeploymentID}
				if start == 0 {
					// Remove the cursor row itself. The next page must still start
					// at the next timestamp/ID pair without an OFFSET skip.
					if _, err := schedules.FinishScheduledRelease(ctx, lastDue.ID, state.ScheduledReleaseCancelled, "test", "", now); err != nil {
						t.Fatal(err)
					}
					if err := watch.RecordPostReleaseRolloutDecision(ctx, lastWatch.DeploymentID, json.RawMessage(`{"rollback_skipped":"test"}`)); err != nil {
						t.Fatal(err)
					}
				}
			}
			if rows, err := schedules.DueScheduledReleasesPage(ctx, now, 2, dueCursor); err != nil || len(rows) != 0 {
				t.Fatalf("end of schedule scan=%+v err=%v", rows, err)
			}
			if rows, err := watch.ListPostReleaseWatchCandidatesPage(ctx, time.Hour, 2, watchCursor); err != nil || len(rows) != 0 {
				t.Fatalf("end of watch scan=%+v err=%v", rows, err)
			}
			if rows, err := schedules.DueScheduledReleasesPage(ctx, now, 10, state.ReleaseScanCursor{}); err != nil || len(rows) != 4 {
				t.Fatalf("wrapped schedule scan=%+v err=%v", rows, err)
			}
			if rows, err := watch.ListPostReleaseWatchCandidatesPage(ctx, time.Hour, 10, state.ReleaseScanCursor{}); err != nil || len(rows) != 4 {
				t.Fatalf("wrapped watch scan=%+v err=%v", rows, err)
			}
			for _, cursor := range []state.ReleaseScanCursor{{At: now}, {ID: allDue[0].ID}, {At: now, ID: "invalid"}} {
				if _, err := schedules.DueScheduledReleasesPage(ctx, now, 2, cursor); !errors.Is(err, state.ErrInvalidArgument) {
					t.Fatalf("invalid schedule cursor accepted: %+v err=%v", cursor, err)
				}
				if _, err := watch.ListPostReleaseWatchCandidatesPage(ctx, time.Hour, 2, cursor); !errors.Is(err, state.ErrInvalidArgument) {
					t.Fatalf("invalid watch cursor accepted: %+v err=%v", cursor, err)
				}
			}
		})
	}
}
