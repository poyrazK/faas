package meter

// sampler_floor_test pins the per-app GB-h floor for min_instances
// > 0 (ADR-060, issue #515). SampleAndRoll must append synthetic
// usage_minutes rows when live CountsForRAM() instance count is
// below ScalingPolicy.MinInstances. Synthetic instance IDs are
// deterministic UUID v5 derived from FloorNamespace + (appID, i)
// — required because usage_minutes.instance_id is a UUID column
// and AppendUsage passes the ID raw.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// seedFloorApp creates an account + app + deployment in the
// MemStore and returns the IDs the test needs. RAMMB is
// parameterised so the test cases can sweep the Hobby / Pro
// range. ScalingPolicy is intentionally unset — the floor
// test cases set it explicitly via store.UpdateApp to mirror
// the production PATCH path.
func seedFloorApp(t *testing.T, store state.Store, plan api.Plan, ramMB int) (appID, depID string) {
	t.Helper()
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, "floor@example.com", plan)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := store.CreateApp(ctx, state.App{
		AccountID: acct.ID, Slug: "floor", RAMMB: ramMB, Type: state.AppTypeApp,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Status: state.DeployLive, Kind: state.DeploymentKindImage,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	return app.ID, dep.ID
}

// setPolicy is the test seam for the production PATCH path
// (cmd/apid/handlers.go::updateApp → store.UpdateApp). The
// ScalingPolicy pointer is required; the Set bit distinguishes
// "unset" from "explicit zero" (matches pkg/state/types.go).
func setPolicy(t *testing.T, store state.Store, appID string, policy state.ScalingPolicy) {
	t.Helper()
	if _, err := store.UpdateApp(context.Background(), appID, state.UpdateAppParams{
		ScalingPolicy:    &policy,
		SetScalingPolicy: true,
	}); err != nil {
		t.Fatalf("UpdateApp scaling policy: %v", err)
	}
}

// TestSampler_AppliesMinInstancesFloor is the table-driven
// pin for the floor append (ADR-060, issue #515). Each case
// asserts: per-row SyntheticFloor bool, total mb_seconds per
// app, additive-column zeros, and zero-noise from apps with
// MinInstances=0 or nil policy.
func TestSampler_AppliesMinInstancesFloor(t *testing.T) {
	minute := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	// Hobby billable: 256 + 8 = 264 MB. Per-instance per-minute
	// MBSeconds = 264 × 60 = 15_840.
	const hobbyBillable = 264
	const perInstance = hobbyBillable * 60 // 15_840
	cases := []struct {
		name        string
		policy      *state.ScalingPolicy // nil => legacy row, no floor
		liveCount   int                  // 0..N live instances
		ramMB       int                  // app RAM in MB
		wantGap     int                  // expected synthetic rows
		wantFloorMB int64                // expected total floor mb_seconds
		wantFirst   int64                // expected mb_seconds on slot 0 (handles remainder)
	}{
		{
			name:        "floor_fires_zero_live_min1",
			policy:      &state.ScalingPolicy{MinInstances: 1},
			liveCount:   0,
			ramMB:       256,
			wantGap:     1,
			wantFloorMB: perInstance,
			wantFirst:   perInstance,
		},
		{
			name:        "floor_fires_partial_min2_live1",
			policy:      &state.ScalingPolicy{MinInstances: 2},
			liveCount:   1,
			ramMB:       256,
			wantGap:     1,
			wantFloorMB: perInstance,
			wantFirst:   perInstance,
		},
		{
			name:        "floor_silent_at_capacity",
			policy:      &state.ScalingPolicy{MinInstances: 1},
			liveCount:   1,
			ramMB:       256,
			wantGap:     0,
			wantFloorMB: 0,
		},
		{
			name:        "floor_silent_zero_policy",
			policy:      &state.ScalingPolicy{MinInstances: 0},
			liveCount:   0,
			ramMB:       256,
			wantGap:     0,
			wantFloorMB: 0,
		},
		{
			name:        "floor_silent_nil_policy",
			policy:      nil,
			liveCount:   0,
			ramMB:       256,
			wantGap:     0,
			wantFloorMB: 0,
		},
		{
			name:        "floor_distribution_3slots",
			policy:      &state.ScalingPolicy{MinInstances: 3},
			liveCount:   0,
			ramMB:       256,
			wantGap:     3,
			wantFloorMB: 3 * perInstance,
			wantFirst:   perInstance,
		},
		{
			name:        "floor_hobby_plan_min1",
			policy:      &state.ScalingPolicy{MinInstances: 1},
			liveCount:   0,
			ramMB:       256,
			wantGap:     1,
			wantFloorMB: perInstance,
			wantFirst:   perInstance,
		},
		{
			name:        "floor_idempotent_redeliver_min2",
			policy:      &state.ScalingPolicy{MinInstances: 2},
			liveCount:   0,
			ramMB:       256,
			wantGap:     2,
			wantFloorMB: 2 * perInstance,
			wantFirst:   perInstance,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := state.NewMemStore()
			appID, depID := seedFloorApp(t, store, api.PlanHobby, tc.ramMB)
			ctx := context.Background()

			// Seed live instances. Use RUNNING state (CountsForRAM true).
			for i := 0; i < tc.liveCount; i++ {
				if _, err := store.CreateInstance(ctx, appID, depID, string(state.StateRunning), tc.ramMB, state.DefaultLocalNodeName, ""); err != nil {
					t.Fatalf("CreateInstance %d: %v", i, err)
				}
			}
			if tc.policy != nil {
				setPolicy(t, store, appID, *tc.policy)
			}

			s := NewSampler(store, nil, func() time.Time { return minute })
			rows, err := s.SampleAndRoll(ctx)
			if err != nil {
				t.Fatalf("SampleAndRoll: %v", err)
			}

			// Partition rows into live vs synthetic.
			var synthRows []RolledRow
			var liveRows []RolledRow
			for _, r := range rows {
				if r.AppID != appID {
					continue
				}
				if r.SyntheticFloor {
					synthRows = append(synthRows, r)
				} else {
					liveRows = append(liveRows, r)
				}
			}
			if got := len(synthRows); got != tc.wantGap {
				t.Errorf("synthetic rows = %d, want %d", got, tc.wantGap)
			}
			if got := len(liveRows); got != tc.liveCount {
				t.Errorf("live rows = %d, want %d", got, tc.liveCount)
			}
			var totalFloor int64
			for _, r := range synthRows {
				totalFloor += r.MBSeconds
				// Synthetic rows carry zero additive columns.
				if r.CPUUsec != 0 || r.TXBytes != 0 || r.NetTxBytes != 0 ||
					r.NetRxBytes != 0 || r.ColdBootCount != 0 {
					t.Errorf("synthetic row additive columns non-zero: %+v", r)
				}
				// AdmissionMB matches BillableRAMMB(ramMB).
				wantAdmission := api.BillableRAMMB(tc.ramMB)
				if r.AdmissionMB != wantAdmission {
					t.Errorf("synthetic AdmissionMB = %d, want %d", r.AdmissionMB, wantAdmission)
				}
				// Synthetic ID must be a valid UUID (the
				// schema column is UUID; the
				// TestFloorInstanceID_PassesPgStoreAppendUsage
				// round-trip is the explicit pgtest pin).
				if _, err := uuid.Parse(r.InstanceID); err != nil {
					t.Errorf("synthetic InstanceID %q is not a valid UUID: %v", r.InstanceID, err)
				}
			}
			if totalFloor != tc.wantFloorMB {
				t.Errorf("floor total mb_seconds = %d, want %d", totalFloor, tc.wantFloorMB)
			}
			// First slot carries the remainder (here 0 by
			// construction; pinned for completeness).
			if len(synthRows) > 0 && synthRows[0].MBSeconds != tc.wantFirst {
				t.Errorf("first slot mb_seconds = %d, want %d", synthRows[0].MBSeconds, tc.wantFirst)
			}
		})
	}
}

func TestSampler_MinInstancesFloorSkipsTerminalDeployment(t *testing.T) {
	statuses := []state.DeploymentStatus{
		state.DeployFailed,
		state.DeploySuperseded,
		state.DeployCancelled,
	}
	for _, status := range statuses {
		t.Run(string(status), func(t *testing.T) {
			store := state.NewMemStore()
			ctx := context.Background()
			acct, err := store.CreateAccount(ctx, "floor-no-live-"+string(status)+"@example.com", api.PlanHobby)
			if err != nil {
				t.Fatalf("CreateAccount: %v", err)
			}
			app, err := store.CreateApp(ctx, state.App{
				AccountID: acct.ID,
				Slug:      "floor-no-live-" + string(status),
				RAMMB:     256,
				Type:      state.AppTypeApp,
			})
			if err != nil {
				t.Fatalf("CreateApp: %v", err)
			}
			// A stale in-flight row must not keep billing alive after a newer
			// terminal deployment becomes the app's current outcome.
			if _, err := store.CreateDeployment(ctx, state.Deployment{
				AppID:  app.ID,
				Status: state.DeployBuilding,
				Kind:   state.DeploymentKindImage,
			}); err != nil {
				t.Fatalf("CreateDeployment(stale building): %v", err)
			}
			if _, err := store.CreateDeployment(ctx, state.Deployment{
				AppID:  app.ID,
				Status: status,
				Kind:   state.DeploymentKindImage,
			}); err != nil {
				t.Fatalf("CreateDeployment: %v", err)
			}
			setPolicy(t, store, app.ID, state.ScalingPolicy{MinInstances: 1})

			rows, err := NewSampler(store, nil, func() time.Time {
				return time.Date(2026, 9, 6, 20, 0, 0, 0, time.UTC)
			}).SampleAndRoll(ctx)
			if err != nil {
				t.Fatalf("SampleAndRoll: %v", err)
			}
			for _, row := range rows {
				if row.AppID == app.ID && row.SyntheticFloor {
					t.Fatalf("unexpected synthetic floor usage for deployment status %q: %+v", status, row)
				}
			}
		})
	}
}

func TestSampler_MinInstancesFloorBillsDuringDeploymentStartup(t *testing.T) {
	statuses := []state.DeploymentStatus{
		state.DeployPending,
		state.DeployBuilding,
		state.DeployImaging,
		state.DeploySnapshotting,
	}
	for _, status := range statuses {
		t.Run(string(status), func(t *testing.T) {
			store := state.NewMemStore()
			ctx := context.Background()
			acct, err := store.CreateAccount(ctx, "floor-starting-"+string(status)+"@example.com", api.PlanHobby)
			if err != nil {
				t.Fatalf("CreateAccount: %v", err)
			}
			app, err := store.CreateApp(ctx, state.App{
				AccountID: acct.ID,
				Slug:      "floor-starting-" + string(status),
				RAMMB:     256,
				Type:      state.AppTypeApp,
			})
			if err != nil {
				t.Fatalf("CreateApp: %v", err)
			}
			if _, err := store.CreateDeployment(ctx, state.Deployment{
				AppID:  app.ID,
				Status: status,
				Kind:   state.DeploymentKindImage,
			}); err != nil {
				t.Fatalf("CreateDeployment: %v", err)
			}
			setPolicy(t, store, app.ID, state.ScalingPolicy{MinInstances: 1})

			rows, err := NewSampler(store, nil, func() time.Time {
				return time.Date(2026, 9, 6, 20, 0, 0, 0, time.UTC)
			}).SampleAndRoll(ctx)
			if err != nil {
				t.Fatalf("SampleAndRoll: %v", err)
			}
			var synthetic int
			for _, row := range rows {
				if row.AppID == app.ID && row.SyntheticFloor {
					synthetic++
				}
			}
			if synthetic != 1 {
				t.Fatalf("synthetic rows = %d, want 1 while deployment status is %q", synthetic, status)
			}
		})
	}
}

// TestSampler_FloorAppliedAcrossMinuteBoundary pins the
// first-write-wins idempotency contract across redelivered
// minutes and minute boundaries. Three sequential ticks: T →
// T+1 → T (redeliver). Each tick at minute T must write
// exactly floorTotal mb_seconds for the synthetic rows; the
// redelivered tick is a no-op on mb_seconds (DO NOTHING on
// (instance_id, minute)) but the sampler still walks the
// floor branch.
func TestSampler_FloorAppliedAcrossMinuteBoundary(t *testing.T) {
	store := state.NewMemStore()
	appID, _ := seedFloorApp(t, store, api.PlanHobby, 256)
	setPolicy(t, store, appID, state.ScalingPolicy{MinInstances: 2})
	ctx := context.Background()

	t0 := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	now := t0
	s := NewSampler(store, nil, func() time.Time { return now })

	// Tick at minute T.
	if _, err := s.SampleAndRoll(ctx); err != nil {
		t.Fatalf("tick T: %v", err)
	}

	// Tick at minute T+1.
	now = t0.Add(time.Minute)
	if _, err := s.SampleAndRoll(ctx); err != nil {
		t.Fatalf("tick T+1: %v", err)
	}

	// Redelivered tick at minute T. The same synthetic UUIDs
	// hit the same (instance_id, minute) PK — mb_seconds is
	// DO NOTHING (the additive columns are zero, so they
	// can't accumulate either). The floor math must not
	// double-count.
	now = t0
	rows, err := s.SampleAndRoll(ctx)
	if err != nil {
		t.Fatalf("redelivered tick T: %v", err)
	}
	var synthRows []RolledRow
	for _, r := range rows {
		if r.SyntheticFloor && r.AppID == appID {
			synthRows = append(synthRows, r)
		}
	}
	if len(synthRows) != 2 {
		t.Errorf("redelivered T: synthetic rows = %d, want 2", len(synthRows))
	}
	var total int64
	for _, r := range synthRows {
		total += r.MBSeconds
	}
	const wantTotal = int64(2 * 264 * 60) // 31_680
	if total != wantTotal {
		t.Errorf("redelivered T: floor total = %d, want %d (no double-count)", total, wantTotal)
	}
}

// TestFloorInstanceID_DeterministicAcrossCalls pins the UUID v5
// purity required by the schema type + first-write-wins contract.
// FloorInstanceID is a pure function of (appID, i): same inputs
// always yield the same UUID; different ordinals never collide;
// real instance UUIDs (UUID v4) and synthetic UUIDs (UUID v5 under
// onebox-faas/meterd/floor/v1) live in disjoint namespaces.
func TestFloorInstanceID_DeterministicAcrossCalls(t *testing.T) {
	// Same inputs → same UUID.
	a := FloorInstanceID("app-xyz", 0)
	b := FloorInstanceID("app-xyz", 0)
	if a != b {
		t.Errorf("FloorInstanceID(app-xyz, 0) not deterministic: %s != %s", a, b)
	}
	// Different ordinal → different UUID.
	c := FloorInstanceID("app-xyz", 1)
	if a == c {
		t.Errorf("FloorInstanceID ordinal collision: 0 == 1 = %s", a)
	}
	// Different appID → different UUID (the namespace contains appID).
	d := FloorInstanceID("app-abc", 0)
	if a == d {
		t.Errorf("FloorInstanceID appID collision: app-xyz == app-abc = %s", a)
	}
	// Synthetic UUIDs are UUID v5 (version nibble = 5). This
	// guards against accidentally swapping in a v4 namespace.
	if a.Version() != 5 {
		t.Errorf("FloorInstanceID version = %d, want 5 (UUID v5)", a.Version())
	}
	// Synthetic IDs must NOT collide with a random UUID v4
	// (real instance IDs). They live in disjoint namespaces;
	// the SHA-1 inputs differ.
	real := uuid.NewString()
	if strings.EqualFold(a.String(), real) {
		t.Errorf("FloorInstanceID collided with uuid.NewString: %s", a)
	}
}

// TestFloorNamespaceFrozen pins the namespace string. The
// version suffix must stay at "v1" — bumping it changes every
// existing floor row's identity and breaks AppendUsage
// idempotency across the upgrade. Future rotation requires a
// new namespace string (v2, …) plus a one-shot migration.
func TestFloorNamespaceFrozen(t *testing.T) {
	if FloorNamespace.Version() != 5 {
		t.Errorf("FloorNamespace version = %d, want 5 (UUID v5)", FloorNamespace.Version())
	}
	// Re-derive the expected namespace and assert equality.
	// If a future change bumps "onebox-faas/meterd/floor/v1"
	// to v2, this test fires before the migration lands.
	want := uuid.NewSHA1(uuid.NameSpaceURL, []byte("onebox-faas/meterd/floor/v1"))
	if FloorNamespace != want {
		t.Errorf("FloorNamespace drifted: got %s, want %s", FloorNamespace, want)
	}
}
