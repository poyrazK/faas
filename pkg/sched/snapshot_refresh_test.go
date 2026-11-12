// adr: 20261106214916

package sched_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// refreshCheckVMM answers SnapshotBackingCurrent from a table and records
// what it was asked.
type refreshCheckVMM struct {
	sched.RoutedVMM
	verdicts map[string]sched.SnapshotBackingVerdict // by storage key
	err      error
	asked    []string // node|storage key
}

func (v *refreshCheckVMM) SnapshotBackingCurrent(_ context.Context, nodeID string, snap sched.SnapshotRef, baseKey string) (sched.SnapshotBackingVerdict, string, error) {
	v.asked = append(v.asked, nodeID+"|"+snap.StorageKey)
	if v.err != nil {
		return sched.SnapshotBackingUnknown, "", v.err
	}
	if baseKey == "" || snap.DeploymentID == "" {
		return sched.SnapshotBackingUnknown, "missing inputs", nil
	}
	if verdict, ok := v.verdicts[snap.StorageKey]; ok {
		return verdict, string(verdict), nil
	}
	return sched.SnapshotBackingUnknown, "unlisted", nil
}

type refreshFixture struct {
	ctx          context.Context
	store        *state.MemStore
	vmm          *refreshCheckVMM
	boots        *sched.FakeVMM
	engine       *sched.Engine
	refresher    *sched.SnapshotRefresher
	nodeA, nodeB state.ComputeNode
}

func newRefreshFixture(t *testing.T) *refreshFixture {
	t.Helper()
	f := &refreshFixture{ctx: context.Background(), store: state.NewMemStore(), boots: &sched.FakeVMM{}}
	f.vmm = &refreshCheckVMM{RoutedVMM: f.boots, verdicts: map[string]sched.SnapshotBackingVerdict{}}
	release := "r1"
	for _, n := range []*state.ComputeNode{&f.nodeA, &f.nodeB} {
		node, err := f.store.CreateComputeNode(f.ctx, state.ComputeNode{
			Name: "node-" + uuid.NewString()[:8], Active: true, ReleaseID: &release, TargetURL: "unix:///run/faas/vmmd.sock",
			VPCPUs: 16, MemMB: 64000, MaxConcurrency: 100, AdmissionCeilingMB: 47600, VCPUBudget: 64,
		})
		if err != nil {
			t.Fatalf("CreateComputeNode: %v", err)
		}
		*n = node
	}
	f.engine = sched.XNewEngine(t, f.store, f.vmm, &sched.FakeNotifier{}, "1.10.0")
	f.refresher = sched.NewSnapshotRefresher(f.engine, nil, nil)
	return f
}

// app seeds an app with a live deployment, an init snapshot captured on node
// and a wake that parked there parkedAgo ago. It returns the app and the
// snapshot's storage key.
func (f *refreshFixture) app(t *testing.T, node string, parkedAgo time.Duration, snap state.Snapshot) (state.App, string) {
	t.Helper()
	acct, err := f.store.CreateAccount(f.ctx, uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := f.store.CreateApp(f.ctx, state.App{AccountID: acct.ID, Slug: "app-" + uuid.NewString()[:8], RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 60})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dep, err := f.store.CreateDeployment(f.ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc", Status: state.DeployLive})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	snap.DeploymentID, snap.Tier, snap.MemBytes = dep.ID, state.SnapshotTierInit, 256<<20
	snap.StorageKey = state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierInit, uuid.NewString())
	if snap.FCVersion == "" {
		snap.FCVersion = "1.10.0"
	}
	row, err := f.store.CreateSnapshot(f.ctx, snap)
	if err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	if err := f.store.RecordSnapshotOrigin(f.ctx, row.ID, node); err != nil {
		t.Fatalf("RecordSnapshotOrigin: %v", err)
	}
	f.park(t, app, dep.ID, node, parkedAgo)
	return app, row.StorageKey
}

func (f *refreshFixture) park(t *testing.T, app state.App, deploymentID, node string, ago time.Duration) {
	t.Helper()
	ins, err := f.store.CreateInstanceWithMode(f.ctx, app.ID, deploymentID, string(state.StateRunning), app.RAMMB, node, uuid.NewString(), string(state.InstanceModeNormal))
	if err != nil {
		t.Fatalf("CreateInstanceWithMode: %v", err)
	}
	if err := f.store.UpdateInstanceStateWithTimestamp(f.ctx, ins.ID, string(state.StateParked), time.Now().UTC().Add(-ago)); err != nil {
		t.Fatalf("park: %v", err)
	}
}

func (f *refreshFixture) shadow(t *testing.T) {
	t.Helper()
	if err := f.refresher.SetMode(api.SnapshotRefreshShadow); err != nil {
		t.Fatal(err)
	}
}

// A pass asks the node's vmmd about each recently parked app whose snapshot
// a wake there would restore, and counts the answers without booting.
func TestSnapshotRefreshPassClassifiesCandidates(t *testing.T) {
	f := newRefreshFixture(t)
	f.shadow(t)
	_, stale := f.app(t, f.nodeA.ID, time.Hour, state.Snapshot{})
	f.vmm.verdicts[stale] = sched.SnapshotBackingStale
	_, current := f.app(t, f.nodeA.ID, 2*time.Hour, state.Snapshot{})
	f.vmm.verdicts[current] = sched.SnapshotBackingCurrent
	// Another Firecracker version is stale without asking vmmd.
	f.app(t, f.nodeA.ID, 3*time.Hour, state.Snapshot{FCVersion: "1.7.0"})
	live, _ := f.app(t, f.nodeA.ID, 4*time.Hour, state.Snapshot{})
	if _, err := f.store.CreateInstanceWithMode(f.ctx, live.ID, "", string(state.StateRunning), 256, f.nodeB.ID, uuid.NewString(), string(state.InstanceModeNormal)); err != nil {
		t.Fatal(err)
	}
	// Outside the window, and parked on the other node: not candidates.
	f.app(t, f.nodeA.ID, 10*24*time.Hour, state.Snapshot{})
	f.app(t, f.nodeB.ID, time.Hour, state.Snapshot{})

	pass, err := f.refresher.Pass(f.ctx, f.nodeA.ID, "r1")
	if err != nil {
		t.Fatalf("Pass: %v", err)
	}
	want := map[string]int{sched.SnapshotRefreshWouldRefresh: 2, sched.SnapshotRefreshCurrent: 1, sched.SnapshotRefreshSkippedLive: 1}
	if pass.Candidates != 4 || len(pass.Outcomes) != len(want) {
		t.Fatalf("pass = %+v, want 4 candidates %v", pass, want)
	}
	for outcome, n := range want {
		if pass.Outcomes[outcome] != n {
			t.Fatalf("pass outcomes = %v, want %v", pass.Outcomes, want)
		}
	}
	// Most recent park first; only the two that needed vmmd were asked.
	if len(f.vmm.asked) != 2 || f.vmm.asked[0] != f.nodeA.ID+"|"+stale || f.vmm.asked[1] != f.nodeA.ID+"|"+current {
		t.Fatalf("vmmd asked %v, want the stale then the current snapshot on node a", f.vmm.asked)
	}
}

// The platform boots only what the customer could boot: an app whose
// deployment is no longer live, or that is no longer active, is skipped.
func TestSnapshotRefreshPassSkipsIneligibleApps(t *testing.T) {
	f := newRefreshFixture(t)
	f.shadow(t)
	app, _ := f.app(t, f.nodeA.ID, time.Hour, state.Snapshot{})
	deps, err := f.store.LiveDeployments(f.ctx, app.ID)
	if err != nil || len(deps) != 1 {
		t.Fatalf("LiveDeployments = %v, %v", deps, err)
	}
	if err := f.store.UpdateDeploymentStatus(f.ctx, deps[0].ID, state.DeploySuperseded, ""); err != nil {
		t.Fatalf("supersede: %v", err)
	}
	pass, err := f.refresher.Pass(f.ctx, f.nodeA.ID, "r1")
	if err != nil || pass.Outcomes[sched.SnapshotRefreshSkippedIneligible] != 1 || len(f.vmm.asked) != 0 {
		t.Fatalf("pass = %+v (err %v), asked %v; want one ineligible app and no vmmd call", pass, err, f.vmm.asked)
	}
}

// One pass per node per release, including when schedd starts; none while
// off; a node whose vmmd did not answer is tried again.
func TestSnapshotRefreshTickRunsOncePerRelease(t *testing.T) {
	f := newRefreshFixture(t)
	_, keyA := f.app(t, f.nodeA.ID, time.Hour, state.Snapshot{})
	_, keyB := f.app(t, f.nodeB.ID, time.Hour, state.Snapshot{})
	f.vmm.verdicts[keyA], f.vmm.verdicts[keyB] = sched.SnapshotBackingCurrent, sched.SnapshotBackingCurrent

	f.refresher.Tick(f.ctx)
	if len(f.vmm.asked) != 0 {
		t.Fatalf("off: vmmd asked %v", f.vmm.asked)
	}
	f.shadow(t)
	f.refresher.Tick(f.ctx)
	if len(f.vmm.asked) != 2 {
		t.Fatalf("first shadow tick asked %v, want both nodes once", f.vmm.asked)
	}
	f.refresher.Tick(f.ctx)
	if len(f.vmm.asked) != 2 {
		t.Fatalf("same releases asked again: %v", f.vmm.asked)
	}

	release := "r2"
	f.nodeA.ReleaseID = &release
	if _, err := f.store.UpsertComputeNode(f.ctx, f.nodeA); err != nil {
		t.Fatal(err)
	}
	f.vmm.err = errors.New("vmmd restarting")
	f.refresher.Tick(f.ctx)
	if len(f.vmm.asked) != 3 || f.vmm.asked[2] != f.nodeA.ID+"|"+keyA {
		t.Fatalf("new release on node a asked %v", f.vmm.asked)
	}
	f.vmm.err = nil
	f.refresher.Tick(f.ctx)
	f.refresher.Tick(f.ctx)
	if len(f.vmm.asked) != 4 || f.vmm.asked[3] != f.nodeA.ID+"|"+keyA {
		t.Fatalf("after vmmd answered: asked %v, want node a retried once", f.vmm.asked)
	}
}

// Turning shadow into on runs the pass again for the same release, so what
// the shadow pass found is refreshed without waiting for the next release.
func TestSnapshotRefreshTickRunsAgainWhenShadowTurnsOn(t *testing.T) {
	f := newRefreshFixture(t)
	f.shadow(t)
	_, key := f.app(t, f.nodeA.ID, time.Hour, state.Snapshot{})
	f.vmm.verdicts[key] = sched.SnapshotBackingCurrent

	f.refresher.Tick(f.ctx)
	f.refresher.Tick(f.ctx)
	if len(f.vmm.asked) != 1 {
		t.Fatalf("shadow: vmmd asked %v, want one pass", f.vmm.asked)
	}
	if err := f.refresher.SetMode(api.SnapshotRefreshOn); err != nil {
		t.Fatal(err)
	}
	f.refresher.Tick(f.ctx)
	f.refresher.Tick(f.ctx)
	if len(f.vmm.asked) != 2 {
		t.Fatalf("on: vmmd asked %v, want one more pass for the same release", f.vmm.asked)
	}
}

// A vmmd that predates the check ends the pass for this release instead of
// being asked every tick.
func TestSnapshotRefreshStopsOnAnOlderVMMD(t *testing.T) {
	f := newRefreshFixture(t)
	f.shadow(t)
	f.app(t, f.nodeA.ID, time.Hour, state.Snapshot{})
	f.app(t, f.nodeA.ID, 2*time.Hour, state.Snapshot{})
	f.vmm.err = sched.ErrSnapshotBackingUnsupported
	f.refresher.Tick(f.ctx)
	f.refresher.Tick(f.ctx)
	if len(f.vmm.asked) != 1 {
		t.Fatalf("vmmd asked %v, want once", f.vmm.asked)
	}
}

func TestSnapshotRefreshSettingsCheckTheirBounds(t *testing.T) {
	f := newRefreshFixture(t)
	if err := f.refresher.SetMode("yes"); err == nil {
		t.Fatal("an unknown mode was accepted")
	}
	if err := f.refresher.SetWindowHours(api.SnapshotRefreshWindowHoursMax + 1); err == nil {
		t.Fatal("window above the parked-row retention accepted")
	}
	if err := f.refresher.SetWindowHours(24); err != nil {
		t.Fatal(err)
	}
	f.shadow(t)
	f.app(t, f.nodeA.ID, 25*time.Hour, state.Snapshot{})
	if pass, err := f.refresher.Pass(f.ctx, f.nodeA.ID, "r1"); err != nil || pass.Candidates != 0 {
		t.Fatalf("24 h window pass = %+v (err %v), want no candidates", pass, err)
	}
}

// backingVmmdClient is a generated vmmd client that answers only
// SnapshotBackingCurrent.
type backingVmmdClient struct {
	vmmdpb.VmmdClient
	resp *vmmdpb.SnapshotBackingCurrentResponse
	err  error
	req  *vmmdpb.SnapshotBackingCurrentRequest
}

func (c *backingVmmdClient) SnapshotBackingCurrent(_ context.Context, req *vmmdpb.SnapshotBackingCurrentRequest, _ ...grpc.CallOption) (*vmmdpb.SnapshotBackingCurrentResponse, error) {
	c.req = req
	return c.resp, c.err
}

// vmmd's answers map to verdicts; a refused key is an unknown verdict, an
// older vmmd is unsupported, and any other failure is an error the pass
// retries.
func TestVMMClientSnapshotBackingCurrentMapsAnswers(t *testing.T) {
	ref := sched.SnapshotRef{DeploymentID: "d1", FCVersion: "1.10.0", StorageKey: "snap/d1/captures/c1/v2/mem", MemBytes: 256 << 20}
	for _, tc := range []struct {
		name    string
		resp    *vmmdpb.SnapshotBackingCurrentResponse
		err     error
		want    sched.SnapshotBackingVerdict
		wantErr error
		failed  bool
	}{
		{name: "current", resp: &vmmdpb.SnapshotBackingCurrentResponse{Verdict: vmmdpb.SnapshotBackingVerdict_SNAPSHOT_BACKING_CURRENT}, want: sched.SnapshotBackingCurrent},
		{name: "stale", resp: &vmmdpb.SnapshotBackingCurrentResponse{Verdict: vmmdpb.SnapshotBackingVerdict_SNAPSHOT_BACKING_STALE, Detail: "kernel moved"}, want: sched.SnapshotBackingStale},
		{name: "unknown", resp: &vmmdpb.SnapshotBackingCurrentResponse{Verdict: vmmdpb.SnapshotBackingVerdict_SNAPSHOT_BACKING_UNKNOWN}, want: sched.SnapshotBackingUnknown},
		{name: "unspecified", resp: &vmmdpb.SnapshotBackingCurrentResponse{}, want: sched.SnapshotBackingUnknown},
		{name: "refused key", err: status.Error(codes.InvalidArgument, "not a capture key"), want: sched.SnapshotBackingUnknown},
		{name: "older vmmd", err: status.Error(codes.Unimplemented, "unknown method"), want: sched.SnapshotBackingUnknown, wantErr: sched.ErrSnapshotBackingUnsupported},
		{name: "unavailable", err: status.Error(codes.Unavailable, "restarting"), want: sched.SnapshotBackingUnknown, failed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cli := &backingVmmdClient{resp: tc.resp, err: tc.err}
			got, _, err := sched.XNewVMMClient(cli).SnapshotBackingCurrent(context.Background(), ref, "base/runtime-node22.ext4")
			if got != tc.want {
				t.Fatalf("verdict = %s, want %s", got, tc.want)
			}
			switch {
			case tc.wantErr != nil && !errors.Is(err, tc.wantErr):
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			case tc.failed && err == nil:
				t.Fatal("an unanswered call returned no error")
			case tc.wantErr == nil && !tc.failed && err != nil:
				t.Fatalf("err = %v, want none", err)
			}
			s := cli.req.GetSnapshot()
			if cli.req.GetBaseKey() != "base/runtime-node22.ext4" || s.GetStorageKey() != ref.StorageKey || s.GetFcVersion() != ref.FCVersion || s.GetDeploymentId() != "d1" || s.GetMemBytes() != ref.MemBytes {
				t.Fatalf("request = %+v", cli.req)
			}
		})
	}
}

// On, a stale snapshot is refreshed: its row is retired, the app boots on
// the node in mode refresh, and the refresh VM is captured and parked.
func TestSnapshotRefreshOnRefreshesAStaleSnapshot(t *testing.T) {
	f := newRefreshFixture(t)
	if err := f.refresher.SetMode(api.SnapshotRefreshOn); err != nil {
		t.Fatal(err)
	}
	app, stale := f.app(t, f.nodeA.ID, time.Hour, state.Snapshot{})
	f.vmm.verdicts[stale] = sched.SnapshotBackingStale
	_, current := f.app(t, f.nodeA.ID, 2*time.Hour, state.Snapshot{})
	f.vmm.verdicts[current] = sched.SnapshotBackingCurrent

	pass, err := f.refresher.Pass(f.ctx, f.nodeA.ID, "r1")
	if err != nil {
		t.Fatalf("Pass: %v", err)
	}
	if pass.Outcomes[sched.SnapshotRefreshRefreshed] != 1 || pass.Outcomes[sched.SnapshotRefreshCurrent] != 1 {
		t.Fatalf("pass outcomes = %v, want one refreshed and one current", pass.Outcomes)
	}
	if cold, snaps, _ := f.boots.XBootCounts(); cold != 1 || snaps != 1 {
		t.Fatalf("vmm cold boots %d, snapshots %d; want one of each", cold, snaps)
	}
	deps, err := f.store.LiveDeployments(f.ctx, app.ID)
	if err != nil || len(deps) != 1 {
		t.Fatalf("LiveDeployments = %v, %v", deps, err)
	}
	live, err := state.LiveSnapshotsForTier(f.ctx, f.store, deps[0].ID, state.SnapshotTierInit)
	if err != nil {
		t.Fatal(err)
	}
	for _, snap := range live {
		if snap.StorageKey == stale {
			t.Fatalf("the stale snapshot %s is still live", stale)
		}
	}
	instances, err := f.store.ListInstancesForApp(f.ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	var refresh []state.Instance
	for _, ins := range instances {
		if state.IsRefresh(ins.Mode) {
			refresh = append(refresh, ins)
		}
	}
	if len(refresh) != 1 || refresh[0].NodeID != f.nodeA.ID || state.IsLive(refresh[0].State) {
		t.Fatalf("refresh instances = %+v, want one on node a, no longer live", refresh)
	}
	// Invariant 4: the parked refresh holds no resident RAM.
	if ram := f.refresher.XResidentRAMForNode(f.nodeA.ID); ram != 0 {
		t.Fatalf("node a resident RAM after the refresh parked = %d MB, want 0", ram)
	}
}

// A node without the headroom defers its refreshes and boots nothing; the
// pass is not recorded, so the next tick tries again.
func TestSnapshotRefreshDefersWithoutHeadroom(t *testing.T) {
	f := newRefreshFixture(t)
	if err := f.refresher.SetMode(api.SnapshotRefreshOn); err != nil {
		t.Fatal(err)
	}
	_, stale := f.app(t, f.nodeA.ID, time.Hour, state.Snapshot{})
	f.vmm.verdicts[stale] = sched.SnapshotBackingStale
	sched.XFillLedger(t, f.refresher, f.nodeA.ID, api.SnapshotRefreshMaxResidencyPercent)

	pass, err := f.refresher.Pass(f.ctx, f.nodeA.ID, "r1")
	if err != nil || pass.Outcomes[sched.SnapshotRefreshDeferred] != 1 {
		t.Fatalf("pass = %+v (err %v), want one deferred", pass, err)
	}
	if cold, _, _ := f.boots.XBootCounts(); cold != 0 {
		t.Fatalf("booted %d refreshes without headroom", cold)
	}
	f.refresher.Tick(f.ctx)
	f.refresher.Tick(f.ctx)
	if n := len(f.vmm.asked); n != 3 {
		t.Fatalf("vmmd asked %d times, want the deferred pass retried on every tick", n)
	}
}

// A refresh VM that outlived SnapshotRefreshMaxRuntime was left by a
// scheduler that stopped mid-refresh; the reaper does not idle-reap the mode,
// so the refresher destroys it and fails its row.
func TestSnapshotRefreshRetiresOrphanedRefreshes(t *testing.T) {
	f := newRefreshFixture(t)
	f.shadow(t)
	app, _ := f.app(t, f.nodeA.ID, time.Hour, state.Snapshot{})
	orphan, err := f.store.CreateInstanceWithMode(f.ctx, app.ID, "", string(state.StateRunning), 256, f.nodeA.ID, uuid.NewString(), string(state.InstanceModeRefresh))
	if err != nil {
		t.Fatal(err)
	}
	f.refresher.Tick(f.ctx)
	if got, _ := f.store.InstanceByID(f.ctx, orphan.ID); !state.IsLive(got.State) {
		t.Fatalf("a young refresh was retired: %s", got.State)
	}
	f.refresher.XSetNow(func() time.Time { return time.Now().Add(api.SnapshotRefreshMaxRuntime + time.Minute) })
	f.refresher.Tick(f.ctx)
	got, err := f.store.InstanceByID(f.ctx, orphan.ID)
	if err != nil || got.State != string(state.StateFailed) {
		t.Fatalf("orphaned refresh = %s (err %v), want failed", got.State, err)
	}
	if _, _, destroys := f.boots.XBootCounts(); destroys != 1 {
		t.Fatalf("destroys = %d, want 1", destroys)
	}
}

// A refresh boot is on the app's wake timeline, labelled as the platform's.
func TestSnapshotRefreshBootIsOnTheTimelineAsThePlatforms(t *testing.T) {
	f := newRefreshFixture(t)
	e := sched.WakeEngineWithEvents(t, f.store, f.vmm, &sched.FakeNotifier{})
	f.refresher = sched.NewSnapshotRefresher(e, nil, nil)
	if err := f.refresher.SetMode(api.SnapshotRefreshOn); err != nil {
		t.Fatal(err)
	}
	app, stale := f.app(t, f.nodeA.ID, time.Hour, state.Snapshot{})
	f.vmm.verdicts[stale] = sched.SnapshotBackingStale
	if pass, err := f.refresher.Pass(f.ctx, f.nodeA.ID, "r1"); err != nil || pass.Outcomes[sched.SnapshotRefreshRefreshed] != 1 {
		t.Fatalf("pass = %+v (err %v), want one refreshed", pass, err)
	}
	instances, err := f.store.ListInstancesForApp(f.ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	wakeID := ""
	for _, ins := range instances {
		if state.IsRefresh(ins.Mode) {
			wakeID = ins.WakeID
		}
	}
	// The events are emitted asynchronously; wait for both boot rows, not
	// for a row count the park's events can also fill.
	var rows []state.Event
	seen := map[string]bool{}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		rows = sched.EventuallyEventsForInstance(t, f.store, wakeID, 1)
		for _, row := range rows {
			if row.Kind != "wake.boot_started" && row.Kind != "wake.boot_completed" {
				continue
			}
			var data map[string]any
			if err := json.Unmarshal(row.Data, &data); err != nil {
				t.Fatal(err)
			}
			if data["trigger"] != sched.TriggerSnapshotRefresh {
				t.Fatalf("%s trigger = %v, want %s", row.Kind, data["trigger"], sched.TriggerSnapshotRefresh)
			}
			seen[row.Kind] = true
		}
		if (seen["wake.boot_started"] && seen["wake.boot_completed"]) || time.Now().After(deadline) {
			break
		}
	}
	if !seen["wake.boot_started"] || !seen["wake.boot_completed"] {
		t.Fatalf("timeline rows %v, want boot started and completed", sched.KindsOf(rows))
	}
}
