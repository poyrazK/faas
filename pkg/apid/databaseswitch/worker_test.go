// adr: 20261023181931 — a rollback switch restores, fences, drains, switches and resumes, and aborts cleanly.
package databaseswitch_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/apid/databaseswitch"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

type fakeDatabases struct {
	restoreErr error
	target     managedpostgres.Database
	named      map[string]bool
	restores   int
}

func (f *fakeDatabases) Get(_ context.Context, _, id string) (managedpostgres.Database, error) {
	if id != f.target.ID {
		return managedpostgres.Database{}, managedpostgres.ErrNotFound
	}
	return f.target, nil
}

func (f *fakeDatabases) FindByName(_ context.Context, _, name string) (managedpostgres.Database, error) {
	if f.named[name] {
		return managedpostgres.Database{ID: uuid.NewString(), Name: name}, nil
	}
	return managedpostgres.Database{}, managedpostgres.ErrNotFound
}

func (f *fakeDatabases) Restore(_ context.Context, r managedpostgres.RestoreDatabaseRequest) (managedpostgres.Database, error) {
	f.restores++
	if f.restoreErr != nil {
		return managedpostgres.Database{}, f.restoreErr
	}
	f.target.Name, f.target.RestoreSourceDatabaseID, f.target.RestorePointInTime = r.Name, r.SourceDatabaseID, r.PointInTime
	return f.target, nil
}

type fakeCutovers struct {
	now         func() time.Time
	cutover     managedpostgres.Cutover
	activateErr error
	fenced      bool
	resumed     bool
	calls       []string
}

func (f *fakeCutovers) Prepare(_ context.Context, r managedpostgres.PrepareCutoverRequest) (managedpostgres.Cutover, error) {
	f.calls = append(f.calls, "prepare")
	f.cutover = managedpostgres.Cutover{ID: r.ID, AccountID: r.AccountID, AppID: r.AppID, Scope: r.Scope, State: managedpostgres.CutoverPrepared}
	return f.cutover, nil
}

func (f *fakeCutovers) Get(context.Context, string, string) (managedpostgres.Cutover, error) {
	if f.cutover.ID == "" {
		return managedpostgres.Cutover{}, managedpostgres.ErrNotFound
	}
	return f.cutover, nil
}

func (f *fakeCutovers) Verify(context.Context, string, string) (managedpostgres.Cutover, error) {
	f.calls = append(f.calls, "verify")
	at := f.now()
	f.cutover.State, f.cutover.VerifiedAt = managedpostgres.CutoverVerified, at
	f.cutover.Credentials = []managedpostgres.CutoverCredential{{ID: uuid.NewString(), State: "sealed", VerifiedAt: at}}
	return f.cutover, nil
}

func (f *fakeCutovers) Cancel(context.Context, string, string) (managedpostgres.Cutover, error) {
	f.calls = append(f.calls, "cancel")
	f.cutover.State = managedpostgres.CutoverCancelled
	f.fenced = false
	return f.cutover, nil
}

func (f *fakeCutovers) Fence(context.Context, string, string) (managedpostgres.CutoverAdmissionFence, error) {
	f.calls = append(f.calls, "fence")
	f.fenced = true
	return managedpostgres.CutoverAdmissionFence{CutoverID: f.cutover.ID, AppID: f.cutover.AppID, FencedAt: f.now()}, nil
}

func (f *fakeCutovers) Activate(context.Context, managedpostgres.CutoverActivation) (managedpostgres.ActivatedCutover, error) {
	f.calls = append(f.calls, "activate")
	if f.activateErr != nil {
		return managedpostgres.ActivatedCutover{}, f.activateErr
	}
	f.cutover.State = managedpostgres.CutoverActivated
	return managedpostgres.ActivatedCutover{SecretsPublished: true}, nil
}

func (f *fakeCutovers) Resume(context.Context, string, string) error {
	f.calls = append(f.calls, "resume")
	f.resumed, f.fenced = true, false
	return nil
}

type fakeDrain struct {
	preview state.DrainPreview
	plan    *state.ManagedPostgresDrainPlan
}

func (f *fakeDrain) PreviewDrainBlockers(context.Context, string) (state.DrainPreview, error) {
	return f.preview, nil
}

func (f *fakeDrain) DrainPlanForCutover(context.Context, string, string, time.Time) (state.ManagedPostgresDrainPlan, bool, error) {
	if f.plan == nil {
		return state.ManagedPostgresDrainPlan{}, false, nil
	}
	return *f.plan, true, nil
}

type fakeReleases struct {
	live        map[string]string // scope -> deployment
	deployments map[string]state.Deployment
	marked      []string
	markErr     error
}

func (f *fakeReleases) LiveDeploymentForScope(_ context.Context, _, scope string) (state.Deployment, error) {
	id, ok := f.live[scope]
	if !ok {
		return state.Deployment{}, state.ErrNotFound
	}
	return f.deployments[id], nil
}

func (f *fakeReleases) DeploymentByID(_ context.Context, id string) (state.Deployment, error) {
	d, ok := f.deployments[id]
	if !ok {
		return state.Deployment{}, state.ErrNotFound
	}
	return d, nil
}

func (f *fakeReleases) PrepareDeploymentRollback(_ context.Context, _, id string) (state.Deployment, error) {
	d := f.deployments[id]
	d.Status = state.DeploySnapshotting
	f.deployments[id] = d
	return d, nil
}

func (f *fakeReleases) MarkDeploymentLive(_ context.Context, id string) error {
	if f.markErr != nil {
		return f.markErr
	}
	f.marked = append(f.marked, id)
	for other, d := range f.deployments {
		if other != id && d.Status == state.DeployLive {
			d.Status, d.TrafficPercent = state.DeploySuperseded, 0
			f.deployments[other] = d
		}
	}
	d := f.deployments[id]
	d.Status, d.TrafficPercent = state.DeployLive, 100
	f.deployments[id] = d
	f.live[d.Scope] = id
	return nil
}

type harness struct {
	t         *testing.T
	clock     time.Time
	store     *state.MemStore
	databases *fakeDatabases
	cutovers  *fakeCutovers
	drain     *fakeDrain
	releases  *fakeReleases
	worker    *databaseswitch.Worker
	sw        state.DatabaseSwitch
	events    []databaseswitch.Event
	snapshots int
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	ctx := context.Background()
	h := &harness{t: t, clock: time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC), store: state.NewMemStore()}
	app, err := h.store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: uuid.NewString(), Slug: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	from := state.Deployment{ID: uuid.NewString(), AppID: app.ID, Scope: "default", Status: state.DeployLive, TrafficPercent: 100, Revision: 42}
	target := state.Deployment{ID: uuid.NewString(), AppID: app.ID, Scope: "default", Status: state.DeploySuperseded, Revision: 41}
	h.releases = &fakeReleases{live: map[string]string{"default": from.ID}, deployments: map[string]state.Deployment{from.ID: from, target.ID: target}}
	h.databases = &fakeDatabases{target: managedpostgres.Database{ID: uuid.NewString(), State: managedpostgres.StateProvisioning}, named: map[string]bool{}}
	h.cutovers = &fakeCutovers{now: h.now}
	h.drain = &fakeDrain{preview: state.DrainPreview{Supported: 2, Parked: 1}}
	h.worker = &databaseswitch.Worker{Store: h.store, Drain: h.drain, Databases: h.databases, Cutovers: h.cutovers, Releases: h.releases,
		InvalidateSnapshots: func(context.Context, string) error { h.snapshots++; return nil },
		Observe:             func(_ context.Context, _ state.DatabaseSwitch, e databaseswitch.Event) { h.events = append(h.events, e) },
		Now:                 h.now}
	h.sw, err = h.store.CreateDatabaseSwitch(ctx, state.DatabaseSwitch{AccountID: app.AccountID, AppID: app.ID, Scope: "default",
		FromDeploymentID: from.ID, TargetDeploymentID: target.ID, UndoneDeploymentIDs: []string{from.ID},
		SourceDatabaseID: uuid.NewString(), TargetName: "orders-before-v42", RestorePoint: h.clock.Add(-time.Hour), CreatedAt: h.clock})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *harness) now() time.Time { return h.clock }

// tick moves the clock past the worker cadence and runs one worker pass.
func (h *harness) tick() state.DatabaseSwitch {
	h.t.Helper()
	h.clock = h.clock.Add(10 * time.Second)
	h.worker.Tick(context.Background())
	sw, err := h.store.DatabaseSwitch(context.Background(), h.sw.AccountID, h.sw.ID)
	if err != nil {
		h.t.Fatal(err)
	}
	h.sw = sw
	return sw
}

// runUntil ticks until the switch reaches phase or the budget runs out.
func (h *harness) runUntil(phase string, budget int) state.DatabaseSwitch {
	h.t.Helper()
	for range budget {
		if sw := h.tick(); sw.Phase == phase {
			return sw
		}
	}
	h.t.Fatalf("switch is %s (%s), want %s", h.sw.Phase, h.sw.FailureCode, phase)
	return h.sw
}

func (h *harness) completeDrain() {
	h.drain.plan = &state.ManagedPostgresDrainPlan{Members: []state.ManagedPostgresDrainPlanMember{
		{State: "retired"}, {State: "retired"}, {State: "blocked", BlockerCode: "parked_instance_unproven"}}}
}

func TestDatabaseSwitchRestoresFencesDrainsSwitchesAndResumes(t *testing.T) {
	h := newHarness(t)
	if sw := h.tick(); sw.Phase != state.DatabaseSwitchRestoring || sw.TargetDatabaseID != h.databases.target.ID {
		t.Fatalf("restore not started: %+v", sw)
	}
	h.databases.target.State = managedpostgres.StateReady
	h.runUntil(state.DatabaseSwitchSettling, 10)
	if !h.cutovers.fenced || h.sw.FencedAt == nil || h.sw.CutoverID != h.sw.ID {
		t.Fatalf("app not fenced by the switch's cutover: %+v", h.sw)
	}
	h.runUntil(state.DatabaseSwitchDraining, 3)
	if h.sw.DrainRequestedAt == nil {
		t.Fatal("drain not requested")
	}
	h.completeDrain()
	h.runUntil(state.DatabaseSwitchCompleted, 6)
	if h.releases.live["default"] != h.sw.TargetDeploymentID || h.sw.ReleaseSwitchedAt == nil || h.sw.ActivatedAt == nil {
		t.Fatalf("target not live or not activated: %+v", h.sw)
	}
	if !h.cutovers.resumed || h.cutovers.fenced || h.snapshots == 0 {
		t.Fatalf("app not resumed with fresh snapshots: resumed=%v fenced=%v snapshots=%d", h.cutovers.resumed, h.cutovers.fenced, h.snapshots)
	}
	if h.sw.Drain != (state.DatabaseSwitchDrain{Retired: 2, Parked: 1}) {
		t.Fatalf("drain counts %+v", h.sw.Drain)
	}
	want := []databaseswitch.Event{databaseswitch.EventFenced, databaseswitch.EventReleaseSwitched, databaseswitch.EventActivated, databaseswitch.EventCompleted}
	if !slices.Equal(h.events, want) {
		t.Fatalf("events %v want %v", h.events, want)
	}
	if h.databases.restores != 1 || slices.Contains(h.cutovers.calls, "cancel") {
		t.Fatalf("calls %v restores %d", h.cutovers.calls, h.databases.restores)
	}
}

func TestDatabaseSwitchRestoreRefusalsFailBeforeAnythingChanges(t *testing.T) {
	for _, tc := range []struct {
		name string
		prep func(h *harness)
		code string
	}{
		{"restore point outside the window", func(h *harness) { h.databases.restoreErr = managedpostgres.ErrInvalid }, "restore_unavailable"},
		{"provisioning closed", func(h *harness) { h.databases.restoreErr = managedpostgres.ErrProvisioningClosed }, "provisioning_closed"},
		{"name taken by another database", func(h *harness) {
			h.databases.restoreErr = managedpostgres.ErrConflict
			h.databases.named["orders-before-v42"] = true
		}, "restore_name_taken"},
		{"restore failed", func(h *harness) { h.databases.target.State = managedpostgres.StateFailed }, "restore_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			tc.prep(h)
			sw := h.runUntil(state.DatabaseSwitchFailed, 5)
			if sw.FailureCode != tc.code || sw.FinishedAt == nil || len(h.cutovers.calls) != 0 || len(h.releases.marked) != 0 {
				t.Fatalf("got %+v calls %v", sw, h.cutovers.calls)
			}
		})
	}
}

func TestDatabaseSwitchCancelRequestedWhileDrainingCancelsTheCutover(t *testing.T) {
	h := newHarness(t)
	h.databases.target.State = managedpostgres.StateReady
	h.runUntil(state.DatabaseSwitchDraining, 12)
	if _, err := h.store.RequestDatabaseSwitchCancel(context.Background(), h.sw.AccountID, h.sw.AppID, h.sw.ID, h.clock); err != nil {
		t.Fatal(err)
	}
	sw := h.runUntil(state.DatabaseSwitchCancelled, 4)
	if sw.FailureCode != "cancelled" || !slices.Contains(h.cutovers.calls, "cancel") || h.cutovers.fenced {
		t.Fatalf("cancel did not unwind: %+v calls %v", sw, h.cutovers.calls)
	}
	if h.releases.live["default"] != sw.FromDeploymentID {
		t.Fatal("the release changed on a cancelled switch")
	}
}

func TestDatabaseSwitchAbortsOnABlockedDrainOrATimeout(t *testing.T) {
	t.Run("members that cannot settle", func(t *testing.T) {
		h := newHarness(t)
		h.databases.target.State = managedpostgres.StateReady
		h.drain.preview = state.DrainPreview{Supported: 1, Blocking: 2}
		h.runUntil(state.DatabaseSwitchSettling, 12)
		h.clock = h.clock.Add(3 * time.Minute)
		sw := h.runUntil(state.DatabaseSwitchFailed, 4)
		if sw.FailureCode != "drain_blocked" || !slices.Contains(h.cutovers.calls, "cancel") {
			t.Fatalf("got %+v", sw)
		}
	})
	t.Run("a plan member the drain cannot retire", func(t *testing.T) {
		h := newHarness(t)
		h.databases.target.State = managedpostgres.StateReady
		h.runUntil(state.DatabaseSwitchDraining, 12)
		h.drain.plan = &state.ManagedPostgresDrainPlan{Members: []state.ManagedPostgresDrainPlanMember{{State: "blocked", BlockerCode: "receipt_invalid"}}}
		sw := h.runUntil(state.DatabaseSwitchFailed, 4)
		if sw.FailureCode != "drain_blocked" || sw.Drain.Blocked != 1 {
			t.Fatalf("got %+v", sw)
		}
	})
	t.Run("a drain that never finishes", func(t *testing.T) {
		h := newHarness(t)
		h.databases.target.State = managedpostgres.StateReady
		h.runUntil(state.DatabaseSwitchDraining, 12)
		h.drain.plan = &state.ManagedPostgresDrainPlan{Members: []state.ManagedPostgresDrainPlanMember{{State: "pending"}}}
		h.clock = h.clock.Add(11 * time.Minute)
		sw := h.runUntil(state.DatabaseSwitchFailed, 4)
		if sw.FailureCode != "drain_timeout" {
			t.Fatalf("got %+v", sw)
		}
	})
}

func TestDatabaseSwitchAbortAfterTheReleaseSwitchRestoresThePreviousRelease(t *testing.T) {
	h := newHarness(t)
	h.databases.target.State = managedpostgres.StateReady
	h.runUntil(state.DatabaseSwitchDraining, 12)
	h.completeDrain()
	h.cutovers.activateErr = &managedpostgres.ActivationBlockedError{Code: managedpostgres.ActivationBindingsChanged}
	sw := h.runUntil(state.DatabaseSwitchFailed, 8)
	if sw.FailureCode != "activation_failed" || sw.ActivatedAt != nil {
		t.Fatalf("got %+v", sw)
	}
	if h.releases.live["default"] != sw.FromDeploymentID || !slices.Equal(h.releases.marked, []string{sw.TargetDeploymentID, sw.FromDeploymentID}) {
		t.Fatalf("previous release not restored: live %s marked %v", h.releases.live["default"], h.releases.marked)
	}
	if !slices.Contains(h.cutovers.calls, "cancel") || h.cutovers.fenced {
		t.Fatalf("cutover not cancelled: %v", h.cutovers.calls)
	}
}

func TestDatabaseSwitchRefusesWhenAnotherReleaseStartedServing(t *testing.T) {
	h := newHarness(t)
	h.databases.target.State = managedpostgres.StateReady
	h.runUntil(state.DatabaseSwitchDraining, 12)
	h.completeDrain()
	other := state.Deployment{ID: uuid.NewString(), AppID: h.sw.AppID, Scope: "default", Status: state.DeployLive, TrafficPercent: 100, Revision: 43}
	h.releases.deployments[other.ID] = other
	h.releases.live["default"] = other.ID
	sw := h.runUntil(state.DatabaseSwitchFailed, 6)
	if sw.FailureCode != "release_changed" || len(h.releases.marked) != 0 || slices.Contains(h.cutovers.calls, "activate") {
		t.Fatalf("got %+v marked %v calls %v", sw, h.releases.marked, h.cutovers.calls)
	}
}

func TestDatabaseSwitchReverifiesStaleEvidenceBeforeActivating(t *testing.T) {
	h := newHarness(t)
	h.databases.target.State = managedpostgres.StateReady
	h.runUntil(state.DatabaseSwitchDraining, 12)
	h.completeDrain()
	h.clock = h.clock.Add(6 * time.Minute) // older than the five-minute evidence window
	verifies := func() int {
		n := 0
		for _, c := range h.cutovers.calls {
			if c == "verify" {
				n++
			}
		}
		return n
	}
	before := verifies()
	h.runUntil(state.DatabaseSwitchCompleted, 8)
	if verifies() <= before {
		t.Fatal("activation ran on stale verification")
	}
}

func TestDatabaseSwitchFinishesAnActivationThatCommittedBeforeACrash(t *testing.T) {
	h := newHarness(t)
	h.databases.target.State = managedpostgres.StateReady
	h.runUntil(state.DatabaseSwitchDraining, 12)
	h.completeDrain()
	h.tick() // switching
	h.tick() // release switched
	h.cutovers.cutover.State = managedpostgres.CutoverActivated
	activations := slices.Index(h.cutovers.calls, "activate")
	sw := h.runUntil(state.DatabaseSwitchCompleted, 4)
	if activations != -1 && slices.Index(h.cutovers.calls, "activate") != activations {
		t.Fatal("activated twice")
	}
	if sw.ActivatedAt == nil || !h.cutovers.resumed {
		t.Fatalf("got %+v", sw)
	}
}
