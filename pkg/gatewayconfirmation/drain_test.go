// adr: 611
package gatewayconfirmation

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/gateway/activity"
	"github.com/onebox-faas/faas/pkg/state"
)

type drainRecorderFixture struct {
	observations []state.RuntimeUpgradeGatewayDrainObservation
	err          error
}

func (r *drainRecorderFixture) RecordRuntimeUpgradeGatewayDrain(_ context.Context, o state.RuntimeUpgradeGatewayDrainObservation) error {
	r.observations = append(r.observations, o)
	return r.err
}

func drainSnapshotFixture() gateway.DeploymentWeightsSnapshot {
	p := &gateway.RuntimeUpgradeDrainPlan{OperationID: uuid.NewString(), DeploymentID: uuid.NewString(), ServingDeploymentID: uuid.NewString(), GatewayRosterRevision: uuid.NewString(), CutoverAt: time.Now().UTC()}
	return gateway.DeploymentWeightsSnapshot{RoutingRevision: strings.Repeat("a", 64), Drain: p, Rows: []gateway.DeploymentWeightsRow{{ID: p.DeploymentID, TrafficPercent: 100}, {ID: p.ServingDeploymentID, TrafficPercent: 0}}}
}

func TestDrainPublicationWaitsForExistingForwardAndRejectsLateEntry(t *testing.T) {
	tracker, err := activity.NewWithFences(uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	snapshot := drainSnapshotFixture()
	app, slot := uuid.NewString(), uuid.NewString()
	recorder := &drainRecorderFixture{}
	finish, ok := tracker.TryBegin(app, snapshot.Drain.ServingDeploymentID)
	if !ok {
		t.Fatal("original forward rejected")
	}
	if err := RecordDrain(t.Context(), recorder, tracker, slot, app, snapshot); err != nil || len(recorder.observations) != 0 {
		t.Fatal("busy predecessor published zero", err)
	}
	if _, ok := tracker.TryBegin(app, snapshot.Drain.ServingDeploymentID); ok {
		t.Fatal("late request crossed installed fence")
	}
	finish()
	if err := RecordDrain(t.Context(), recorder, tracker, slot, app, snapshot); err != nil || len(recorder.observations) != 1 {
		t.Fatal(err, recorder.observations)
	}
	o := recorder.observations[0]
	if o.AppID != app || o.SlotID != slot || o.RoutingRevision != snapshot.RoutingRevision || o.ServingDeploymentID != snapshot.Drain.ServingDeploymentID || o.FenceID == "" {
		t.Fatal(o)
	}
	fence, _, _ := tracker.ObserveFence(app)
	if err := RecordDrain(t.Context(), recorder, tracker, slot, app, snapshot); err != nil || recorder.observations[1].FenceID != fence.ID {
		t.Fatal("repair reopened/replaced exact fence", err)
	}
}

func TestDrainRollbackAndReactivationCannotReuseClosedZero(t *testing.T) {
	tracker, err := activity.NewWithFences(uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	snapshot := drainSnapshotFixture()
	app, slot := uuid.NewString(), uuid.NewString()
	recorder := &drainRecorderFixture{}
	if err := RecordDrain(t.Context(), recorder, tracker, slot, app, snapshot); err != nil {
		t.Fatal(err)
	}
	first := recorder.observations[0]
	rollback := gateway.DeploymentWeightsSnapshot{RoutingRevision: strings.Repeat("b", 64), Rows: []gateway.DeploymentWeightsRow{{ID: snapshot.Drain.ServingDeploymentID, TrafficPercent: 100}}}
	if err := RecordDrain(t.Context(), recorder, tracker, slot, app, rollback); err != nil {
		t.Fatal(err)
	}
	finish, ok := tracker.TryBegin(app, snapshot.Drain.ServingDeploymentID)
	if !ok {
		t.Fatal("rollback admission still closed")
	}
	snapshot.RoutingRevision = strings.Repeat("c", 64)
	if err := RecordDrain(t.Context(), recorder, tracker, slot, app, snapshot); err != nil || len(recorder.observations) != 1 {
		t.Fatal("reactivation certified an existing rollback request", err)
	}
	finish()
	if err := RecordDrain(t.Context(), recorder, tracker, slot, app, snapshot); err != nil {
		t.Fatal(err)
	}
	latest := recorder.observations[1]
	if latest.FenceID == first.FenceID || latest.RoutingRevision == first.RoutingRevision || latest.ActivityVersion == first.ActivityVersion {
		t.Fatal("reactivation reused old proof", first, latest)
	}
}

func TestDrainFailureUnknownCoverageAndUnreviewedRosterPublishNoSuccess(t *testing.T) {
	tracker, err := activity.NewWithFences(uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	snapshot := drainSnapshotFixture()
	app, slot := uuid.NewString(), uuid.NewString()
	sentinel := errors.New("synthetic database failure")
	recorder := &drainRecorderFixture{err: sentinel}
	if err := RecordDrain(t.Context(), recorder, tracker, slot, app, snapshot); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	if _, ok := tracker.TryBegin(app, snapshot.Drain.ServingDeploymentID); ok {
		t.Fatal("publication failure reopened admission")
	}
	_, _ = tracker.TryBegin("", "")
	recorder.observations = nil
	recorder.err = nil
	if err := RecordDrain(t.Context(), recorder, tracker, slot, app, snapshot); err != nil || len(recorder.observations) != 0 {
		t.Fatal("unknown coverage published success", err)
	}
	fresh, err := activity.NewWithFences(uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Drain.GatewayRosterRevision = ""
	if err := RecordDrain(t.Context(), recorder, fresh, slot, app, snapshot); err != nil || len(recorder.observations) != 0 {
		t.Fatal("unreviewed roster published success", err)
	}
	snapshot.Rows[0].TrafficPercent = 50
	if err := RecordDrain(t.Context(), recorder, fresh, slot, app, snapshot); !errors.Is(err, state.ErrConflict) {
		t.Fatal("mismatched installed weights accepted", err)
	}
}

type drainRepairFixture struct{ apps []string }

func (r *drainRepairFixture) ListRuntimeUpgradeGatewayRepairApps(context.Context, string) ([]string, error) {
	return nil, nil
}
func (r *drainRepairFixture) PruneExpiredRuntimeUpgradeGatewayReceipts(context.Context) error {
	return nil
}
func (r *drainRepairFixture) PruneExpiredRuntimeUpgradeGatewayDrains(context.Context) error {
	return nil
}
func (r *drainRepairFixture) ListRuntimeUpgradeGatewayDrainRepairApps(_ context.Context, after string) ([]string, error) {
	var out []string
	for _, id := range r.apps {
		if id > after {
			out = append(out, id)
		}
	}
	if len(out) > api.RuntimeUpgradeGatewayRepairBatch {
		out = out[:api.RuntimeUpgradeGatewayRepairBatch]
	}
	return out, nil
}

func TestDrainRepairRetainsOldFencesWithoutOmittingPagedRecentApps(t *testing.T) {
	tracker, err := activity.NewWithFences(uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	store := &drainRepairFixture{}
	for range api.RuntimeUpgradeGatewayRepairBatch * 2 {
		store.apps = append(store.apps, uuid.NewString())
	}
	slices.Sort(store.apps)
	var held []string
	for range api.RuntimeUpgradeGatewayRepairBatch + 1 {
		id := uuid.NewString()
		held = append(held, id)
		if _, err := tracker.InstallFence(id, uuid.NewString(), strings.Repeat("a", 64), "binding"); err != nil {
			t.Fatal(err)
		}
	}
	repair := DrainRepair{Store: store, Tracker: tracker}
	cursor := ""
	var visited []string
	for {
		page, err := repair.ListRuntimeUpgradeGatewayRepairApps(t.Context(), cursor)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) > api.RuntimeUpgradeGatewayRepairBatch {
			t.Fatal("unbounded page")
		}
		visited = append(visited, page...)
		if len(page) < api.RuntimeUpgradeGatewayRepairBatch {
			break
		}
		cursor = page[len(page)-1]
	}
	want := append(slices.Clone(store.apps), held...)
	slices.Sort(want)
	want = slices.Compact(want)
	if !slices.Equal(visited, want) {
		t.Fatal("recent or held app omitted", len(visited), len(want))
	}
}
