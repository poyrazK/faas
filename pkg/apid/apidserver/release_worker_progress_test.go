// adr: 1445, 20261021152201 — bounded release workers must progress past older candidates.
package apidserver_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/canary"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestScheduledReleaseProgressesPastWaitingBuilds(t *testing.T) {
	for _, blocked := range []int{0, api.ScheduledReleaseBatch, 2 * api.ScheduledReleaseBatch} {
		t.Run(fmt.Sprint(blocked), func(t *testing.T) {
			f := newScheduleFixture(t, "audit5-scheduled")
			ctx, store, now := t.Context(), *f.e.Store(), time.Now().UTC()
			var waiting []state.Deployment
			for i := 0; i < blocked; i++ {
				acct, err := store.CreateAccount(ctx, fmt.Sprintf("schedule-block-%d@example.test", i), api.PlanPro)
				if err != nil {
					t.Fatal(err)
				}
				app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: fmt.Sprintf("blocked-%d", i), Type: state.AppTypeApp, Status: state.AppActive})
				if err != nil {
					t.Fatal(err)
				}
				dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployImaging, ImageDigest: "sha256:" + strings.Repeat("a", 64), CreatedAt: now.Add(-10 * time.Minute), TrafficPercentExplicit: true})
				if err != nil {
					t.Fatal(err)
				}
				_, _, err = store.ScheduleRelease(ctx, state.ScheduledRelease{AccountID: acct.ID, AppID: app.ID, DeploymentID: dep.ID, ReleaseAt: now.Add(-2 * time.Minute), ScheduledBy: "user:release-worker"}, "", "", now.Add(-10*time.Minute))
				if err != nil {
					t.Fatal(err)
				}
				waiting = append(waiting, dep)
			}
			target := f.parked(t, 'd')
			sr, _, err := store.ScheduleRelease(ctx, state.ScheduledRelease{AccountID: (*f.e.Acct()).ID, AppID: f.app.ID, DeploymentID: target.ID, ReleaseAt: now.Add(-time.Minute), ScheduledBy: "user:release-worker"}, "", "", now.Add(-10*time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			(*f.e.S()).StartDueScheduledReleases(ctx, now)
			if blocked >= api.ScheduledReleaseBatch && f.read(t, target.ID).TrafficPercent != 0 {
				t.Fatal("first sweep exceeded its candidate budget")
			}
			for i := 1; i < 4; i++ {
				(*f.e.S()).StartDueScheduledReleases(ctx, now.Add(time.Duration(i)*api.ScheduledReleaseCheckInterval))
			}
			got, err := store.ScheduledRelease(ctx, sr.ID)
			if err != nil {
				t.Fatal(err)
			}
			dep, err := store.DeploymentByID(ctx, target.ID)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("older waiting schedules=%d target after four sweeps: state=%s traffic=%d", blocked, got.State, dep.TrafficPercent)
			if got.State != state.ScheduledReleaseStarted || dep.TrafficPercent != 100 {
				t.Fatal("ready due release never reached behind unrelated waiting schedules")
			}
			if len(waiting) == 0 {
				return
			}
			// A previously visited release becomes ready after its build.
			if err := store.MarkDeploymentLive(ctx, waiting[0].ID); err != nil {
				t.Fatal(err)
			}
			for i := 4; i < 9; i++ {
				(*f.e.S()).StartDueScheduledReleases(ctx, now.Add(time.Duration(i)*api.ScheduledReleaseCheckInterval))
			}
			old, err := store.LatestScheduledRelease(ctx, waiting[0].ID)
			if err != nil || old.State != state.ScheduledReleaseStarted {
				t.Fatalf("wrapped scan did not revisit a now-ready release: %+v %v", old, err)
			}
		})
	}
}

func TestPostReleaseWatchProgressesPastHealthyReleases(t *testing.T) {
	for _, older := range []int{0, api.PostReleaseWatchBatchSize, 2 * api.PostReleaseWatchBatchSize} {
		t.Run(fmt.Sprint(older), func(t *testing.T) {
			f := newPostReleaseFixture(t, "audit5-watch", true, time.Minute)
			ctx, store, now := t.Context(), *f.e.Store(), time.Now().UTC()
			for i := 0; i < older; i++ {
				acct, err := store.CreateAccount(ctx, fmt.Sprintf("watch-healthy-%d@example.test", i), api.PlanPro)
				if err != nil {
					t.Fatal(err)
				}
				app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: fmt.Sprintf("healthy-%d", i), Type: state.AppTypeApp, Status: state.AppActive})
				if err != nil {
					t.Fatal(err)
				}
				previous, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployBuilding, ImageDigest: "sha256:" + strings.Repeat("a", 64), CreatedAt: now.Add(-time.Hour)})
				if err != nil {
					t.Fatal(err)
				}
				if err := store.MarkDeploymentLive(ctx, previous.ID); err != nil {
					t.Fatal(err)
				}
				current, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployBuilding, ImageDigest: "sha256:" + strings.Repeat("b", 64), RollbackOn5xx: true, CreatedAt: now.Add(-10 * time.Minute)})
				if err != nil {
					t.Fatal(err)
				}
				if err := store.MarkDeploymentLive(ctx, current.ID); err != nil {
					t.Fatal(err)
				}
				if err := store.MarkDeploymentSuperseded(ctx, previous.ID); err != nil {
					t.Fatal(err)
				}
				completed, started := now.Add(-2*time.Minute), now.Add(-6*time.Minute)
				if _, err := store.SafedeployStampRollout(ctx, current.ID, "complete", &started, &completed, nil, ""); err != nil {
					t.Fatal(err)
				}
				f.telemetry.windows[previous.ID] = canary.HealthWindow{Requests: 400, ServerErrors: 2, P95LatencyMS: 40}
				f.telemetry.windows[current.ID] = canary.HealthWindow{Requests: 300, ServerErrors: 1, P95LatencyMS: 45}
			}
			f.telemetry.windows[f.release.ID] = canary.HealthWindow{Requests: 300, ServerErrors: 1, P95LatencyMS: 900}
			counter := &releaseWatchReadCounter{telemetry: f.telemetry, reads: map[string]int{}}
			(*f.e.S()).SetPostReleaseTelemetry(counter)
			oldest, err := store.ListPostReleaseWatchCandidates(ctx, api.PostReleaseWatchWindow, 1)
			if err != nil || len(oldest) != 1 {
				t.Fatalf("watch fixture candidates=%+v err=%v", oldest, err)
			}
			f.sweep(t)
			if older >= api.PostReleaseWatchBatchSize && f.read(t, f.release.ID).LastAutoRollbackReason != "" {
				t.Fatal("first watch sweep exceeded its candidate budget")
			}
			for i := 1; i < 4; i++ {
				f.sweep(t)
			}
			got := f.read(t, f.release.ID)
			t.Logf("older healthy watched releases=%d target after four sweeps: auto rollback=%q traffic=%d telemetry reads=%d", older, got.LastAutoRollbackReason, got.TrafficPercent, f.telemetry.reads)
			if got.LastAutoRollbackReason != string(state.AutoRollbackReasonPostReleaseRegression) {
				t.Error("regressed release never evaluated behind unrelated healthy releases")
			}
			if older > 0 && counter.reads[oldest[0].DeploymentID] < 2 {
				t.Fatal("wrapped watch did not reevaluate an earlier healthy release")
			}
			if older == 0 {
				return
			}
			// Finish the current pass, then interrupt the next page at its
			// first read. The unattempted second row must survive the timeout.
			f.sweep(t)
			f.sweep(t)
			first, err := store.ListPostReleaseWatchCandidates(ctx, api.PostReleaseWatchWindow, 2)
			if err != nil || len(first) != 2 {
				t.Fatalf("interrupted scan fixture=%+v err=%v", first, err)
			}
			before := counter.reads[first[1].DeploymentID]
			interrupted, cancel := context.WithCancel(ctx)
			defer cancel()
			counter.cancelNext = cancel
			if err := (*f.e.S()).PostReleaseWatchSweep(interrupted, now); !errors.Is(err, context.Canceled) {
				t.Fatalf("interrupted sweep error=%v", err)
			}
			f.sweep(t)
			if counter.reads[first[1].DeploymentID] != before+1 {
				t.Fatal("interrupted scan skipped the page's unattempted row")
			}
		})
	}
}

type releaseWatchReadCounter struct {
	telemetry  *postReleaseTelemetry
	reads      map[string]int
	cancelNext context.CancelFunc
}

func (c *releaseWatchReadCounter) RequestTelemetryCircuitBreakerSummary(ctx context.Context, appID, depID string, since, until time.Time) (int64, int64, float64, int64, float64, int64, int64, error) {
	c.reads[depID]++
	if c.cancelNext != nil {
		cancel := c.cancelNext
		c.cancelNext = nil
		cancel()
		return 0, 0, 0, 0, 0, 0, 0, ctx.Err()
	}
	return c.telemetry.RequestTelemetryCircuitBreakerSummary(ctx, appID, depID, since, until)
}
