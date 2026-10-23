package conformance

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

// parkedDeployment creates a release that is live without traffic, the way
// a scheduled deploy parks one.
func parkedDeployment(t *testing.T, fx *Fixture) state.Deployment {
	t.Helper()
	d, err := fx.Store.CreateDeployment(fx.Ctx, state.Deployment{
		AppID: fx.App.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:parked-" + uuid.NewString(),
		Status: state.DeployPending, TrafficPercent: 0, TrafficPercentExplicit: true,
	})
	if err != nil {
		t.Fatalf("CreateDeployment(parked): %v", err)
	}
	if err := fx.Store.MarkDeploymentLive(fx.Ctx, d.ID); err != nil {
		t.Fatalf("MarkDeploymentLive(parked): %v", err)
	}
	return d
}

// testScheduledReleases pins ADR-20261021152201's bookkeeping on both
// stores: one pending schedule per app, due listing, ending a schedule once,
// and a start that gives a parked release its first canary stage in the
// traffic change's transaction.
func testScheduledReleases(t *testing.T, fx *Fixture) {
	t.Helper()
	store, ok := fx.Store.(state.ScheduledReleaseStore)
	if !ok {
		t.Fatalf("%T does not implement state.ScheduledReleaseStore", fx.Store)
	}
	now := time.Now().UTC().Truncate(time.Second)
	first, second := parkedDeployment(t, fx), parkedDeployment(t, fx)
	notify := uuid.NewString()
	schedule := func(d state.Deployment, at time.Time) (state.ScheduledRelease, []state.ScheduledRelease) {
		t.Helper()
		created, replaced, err := store.ScheduleRelease(fx.Ctx, state.ScheduledRelease{
			AccountID: fx.Account.ID, AppID: fx.App.ID, DeploymentID: d.ID, ReleaseAt: at,
			Canary: json.RawMessage(`{"preset":"balanced"}`), NotifyChannelIDs: []string{notify},
			OverrideFreeze: "launch night", OverrideFreezeIDs: []string{uuid.NewString()}, ScheduledBy: "user:conformance",
		}, "replaced", "a newer schedule replaced it", now)
		if err != nil {
			t.Fatalf("ScheduleRelease: %v", err)
		}
		return created, replaced
	}
	early, replaced := schedule(first, now.Add(time.Hour))
	if len(replaced) != 0 || early.State != state.ScheduledReleasePending || early.Revision != first.Revision ||
		!early.ReleaseAt.Equal(now.Add(time.Hour)) || string(early.Canary) == "" || len(early.NotifyChannelIDs) != 1 ||
		early.NotifyChannelIDs[0] != notify || early.OverrideFreeze != "launch night" || len(early.OverrideFreezeIDs) != 1 || early.StartedAt != nil {
		t.Fatalf("scheduled = %+v, replaced = %+v", early, replaced)
	}
	late, replaced := schedule(second, now.Add(2*time.Hour))
	if len(replaced) != 1 || replaced[0].ID != early.ID || replaced[0].State != state.ScheduledReleaseCancelled || replaced[0].OutcomeCode != "replaced" {
		t.Fatalf("second schedule replaced = %+v", replaced)
	}
	listed, err := store.ListScheduledReleases(fx.Ctx, fx.Account.ID, fx.App.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 || listed[0].ID != late.ID || listed[1].ID != early.ID {
		t.Fatalf("listed = %+v", listed)
	}
	if latest, err := store.LatestScheduledRelease(fx.Ctx, first.ID); err != nil || latest.ID != early.ID {
		t.Fatalf("latest for first = %+v, %v", latest, err)
	}
	if _, err := store.LatestScheduledRelease(fx.Ctx, fx.Deployment.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("latest for an unscheduled release err = %v, want ErrNotFound", err)
	}
	if due, err := store.DueScheduledReleases(fx.Ctx, now.Add(time.Hour), 10); err != nil || len(due) != 0 {
		t.Fatalf("due before release_at = %+v, %v", due, err)
	}
	due, err := store.DueScheduledReleases(fx.Ctx, now.Add(2*time.Hour), 10)
	if err != nil || len(due) != 1 || due[0].ID != late.ID {
		t.Fatalf("due at release_at = %+v, %v", due, err)
	}
	if _, err := store.FinishScheduledRelease(fx.Ctx, early.ID, state.ScheduledReleaseBlocked, "release_frozen", "", now); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("finishing a cancelled schedule err = %v, want ErrNotFound", err)
	}

	startedAt := now.Add(2 * time.Hour)
	ladder := state.ScheduledReleaseLadder{Preset: "balanced", TotalSteps: 3, FirstPercent: 10}
	started, err := store.StartScheduledRelease(fx.Ctx, state.ScheduledReleaseStart{
		ScheduleID: late.ID, DeploymentID: second.ID, Ladder: ladder, Now: startedAt,
	})
	if err != nil {
		t.Fatalf("StartScheduledRelease: %v", err)
	}
	if started.TrafficPercent != 10 || started.TrafficPercentExplicit || started.CanaryTotalSteps != 3 || started.CanaryStep != 0 ||
		started.CanaryPreset != "balanced" || state.NormalizeRolloutState(started.RolloutState) != "rolling_out" ||
		started.RolloutStartedAt == nil || !started.RolloutStartedAt.Equal(startedAt) || started.RolloutCompletedAt != nil {
		t.Fatalf("started release = %+v", started)
	}
	serving, err := fx.Store.DeploymentByID(fx.Ctx, fx.Deployment.ID)
	if err != nil || serving.TrafficPercent != 90 {
		t.Fatalf("serving release after start = %d, %v; want 90%%", serving.TrafficPercent, err)
	}
	record, err := store.ScheduledRelease(fx.Ctx, late.ID)
	if err != nil || record.State != state.ScheduledReleaseStarted || record.StartedAt == nil || !record.StartedAt.Equal(startedAt) {
		t.Fatalf("started schedule = %+v, %v", record, err)
	}
	if _, err := store.StartScheduledRelease(fx.Ctx, state.ScheduledReleaseStart{
		ScheduleID: late.ID, DeploymentID: second.ID, Ladder: ladder, Now: startedAt,
	}); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("second start err = %v, want ErrNotFound", err)
	}

	// The canary now in flight refuses another start, and the schedule stays
	// pending for the next pass.
	again, _ := schedule(first, now)
	if _, err := store.StartScheduledRelease(fx.Ctx, state.ScheduledReleaseStart{
		ScheduleID: again.ID, DeploymentID: first.ID, Now: startedAt,
	}); !errors.Is(err, state.ErrTrafficChangeDuringCanary) {
		t.Fatalf("start during a canary err = %v, want ErrTrafficChangeDuringCanary", err)
	}
	if record, err := store.ScheduledRelease(fx.Ctx, again.ID); err != nil || record.State != state.ScheduledReleasePending {
		t.Fatalf("schedule after a refused start = %+v, %v", record, err)
	}
	notParked, _ := schedule(second, now)
	if _, err := store.StartScheduledRelease(fx.Ctx, state.ScheduledReleaseStart{
		ScheduleID: notParked.ID, DeploymentID: second.ID, Now: startedAt,
	}); !errors.Is(err, state.ErrScheduledReleaseNotParked) {
		t.Fatalf("start of a release with traffic err = %v, want ErrScheduledReleaseNotParked", err)
	}
	blocked, err := store.FinishScheduledRelease(fx.Ctx, notParked.ID, state.ScheduledReleaseBlocked, "release_frozen", "frozen for the launch", now)
	if err != nil || blocked.State != state.ScheduledReleaseBlocked || blocked.OutcomeCode != "release_frozen" || blocked.OutcomeDetail != "frozen for the launch" {
		t.Fatalf("blocked = %+v, %v", blocked, err)
	}
}
