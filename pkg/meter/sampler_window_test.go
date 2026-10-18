package meter

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

// historyOnlyStore exposes only state.Store, hiding the billing-window listing
// so the sampler falls back to reading each app's full instance history.
type historyOnlyStore struct {
	Store state.Store
}

// windowListingStore counts billing-window listings and full-history reads.
type windowListingStore struct {
	MemStore     *state.MemStore
	windowReads  int
	historyReads int
}

func (s *windowListingStore) ListInstancesForApp(ctx context.Context, appID string) ([]state.Instance, error) {
	s.historyReads++
	return s.MemStore.ListInstancesForApp(ctx, appID)
}

func (s *windowListingStore) ListBillingWindowInstances(ctx context.Context, since, end time.Time) ([]state.Instance, error) {
	s.windowReads++
	return s.MemStore.ListBillingWindowInstances(ctx, since, end)
}

type windowFixture struct {
	store   *state.MemStore
	minute  time.Time
	usage   map[string]UsageDeltas
	longIDs []string
}

// seedBillingWindowFixture builds apps whose instances span the billing
// window: running, parked a minute ago, parked a month ago, failed a minute
// ago, failed a month ago, plus an app billed only through its warm floor.
// MemStore stamps start times with the wall clock, so the rolled minute is a
// month after seeding.
func seedBillingWindowFixture(t *testing.T) windowFixture {
	t.Helper()
	ctx := context.Background()
	store := state.NewMemStore()
	seeded := time.Now().UTC()
	minute := seeded.Add(30 * 24 * time.Hour).Truncate(time.Minute)
	acct, err := store.CreateAccount(ctx, "window@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	f := windowFixture{store: store, minute: minute, usage: map[string]UsageDeltas{}}
	newApp := func(slug string) (state.App, state.Deployment) {
		app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: slug, RAMMB: 512, Type: state.AppTypeApp})
		if err != nil {
			t.Fatal(err)
		}
		dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Status: state.DeployLive, Kind: state.DeploymentKindImage})
		if err != nil {
			t.Fatal(err)
		}
		return app, dep
	}
	newInstance := func(app state.App, dep state.Deployment, st state.State) state.Instance {
		ins, err := store.CreateInstance(ctx, app.ID, dep.ID, string(st), 512, state.DefaultLocalNodeName, "")
		if err != nil {
			t.Fatal(err)
		}
		return ins
	}

	app, dep := newApp("busy")
	running := newInstance(app, dep, state.StateRunning)
	f.usage[running.ID] = UsageDeltas{TXBytes: 10, Requests: 3}
	justParked := newInstance(app, dep, state.StateRunning)
	if err := store.UpdateInstanceStateWithTimestamp(ctx, justParked.ID, string(state.StateParked), minute.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	f.usage[justParked.ID] = UsageDeltas{TXBytes: 20, Requests: 1, ColdBootCount: 1}
	for range 3 {
		old := newInstance(app, dep, state.StateRunning)
		if err := store.UpdateInstanceStateWithTimestamp(ctx, old.ID, string(state.StateParked), seeded.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		f.longIDs = append(f.longIDs, old.ID)
	}
	justFailed := newInstance(app, dep, state.StateRunning)
	if err := store.UpdateInstanceStateToTerminal(ctx, justFailed.ID, string(state.StateFailed), minute.Add(-2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	f.usage[justFailed.ID] = UsageDeltas{TXBytes: 5, Requests: 1}
	longFailed := newInstance(app, dep, state.StateRunning)
	if err := store.UpdateInstanceStateToTerminal(ctx, longFailed.ID, string(state.StateFailed), seeded.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	f.longIDs = append(f.longIDs, longFailed.ID)

	floorApp, floorDep := newApp("floored")
	setPolicy(t, store, floorApp.ID, state.ScalingPolicy{MinInstances: 1})
	oldFloor := newInstance(floorApp, floorDep, state.StateRunning)
	if err := store.UpdateInstanceStateWithTimestamp(ctx, oldFloor.ID, string(state.StateParked), seeded.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	f.longIDs = append(f.longIDs, oldFloor.ID)
	return f
}

func (f windowFixture) egress() *fakeEgressSource {
	e := newFakeEgressSource()
	for id, usage := range f.usage {
		e.SetUsage(id, usage)
	}
	return e
}

func rolledRowKeys(rows []RolledRow) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, fmt.Sprintf("%+v", row))
	}
	sort.Strings(out)
	return out
}

// The billing-window listing must bill exactly what reading every app's full
// history billed: the same rows, mb_seconds, telemetry drains and floor rows.
func TestSampleAndRollWindowMatchesFullHistory(t *testing.T) {
	f := seedBillingWindowFixture(t)
	ctx := context.Background()
	clock := func() time.Time { return f.minute }

	history, err := NewSamplerWithEgress(statetest.NewStore(&historyOnlyStore{Store: f.store}), nil, f.egress(), clock).SampleAndRoll(ctx)
	if err != nil {
		t.Fatalf("full-history roll: %v", err)
	}
	counting := &windowListingStore{MemStore: f.store}
	windowed, err := NewSamplerWithEgress(statetest.NewMem(counting), nil, f.egress(), clock).SampleAndRoll(ctx)
	if err != nil {
		t.Fatalf("window roll: %v", err)
	}
	if got, want := rolledRowKeys(windowed), rolledRowKeys(history); !reflect.DeepEqual(got, want) {
		t.Fatalf("window roll differs from full-history roll:\nwindow:  %v\nhistory: %v", got, want)
	}
	if len(history) < 4 {
		t.Fatalf("fixture produced %d rows; want running, two drains and a floor row", len(history))
	}
	if counting.windowReads != 1 || counting.historyReads != 0 {
		t.Fatalf("window roll made %d window and %d history reads, want 1 and 0", counting.windowReads, counting.historyReads)
	}
}

// Instances inactive since before the catch-up window stay out of the roll.
func TestBillingWindowSkipsLongInactiveInstances(t *testing.T) {
	f := seedBillingWindowFixture(t)
	rows, err := f.store.ListBillingWindowInstances(context.Background(), f.minute.Add(-api.MeterCatchUpWindow), f.minute.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, ins := range rows {
		listed[ins.ID] = true
	}
	for _, id := range f.longIDs {
		if listed[id] {
			t.Errorf("instance %s inactive for a month was listed", id)
		}
	}
	for id := range f.usage {
		if !listed[id] {
			t.Errorf("instance %s with pending telemetry was not listed", id)
		}
	}
}
