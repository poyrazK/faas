package sched

// adr: 343 — terminal init capture is distinct from snapshot reuse and has
// a stable before_checkpoint failure reason on both prime and later park.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

func TestTerminalInitSnapshotMetricsFollowEngineOutcome(t *testing.T) {
	for _, tc := range []struct {
		name, site, outcome string
		snapshotErr         error
		reuse, destroyFails bool
	}{
		{name: "prime capture", site: wire.InitSnapshotSitePrime, outcome: wire.InitSnapshotOutcomeCaptured},
		{name: "prime hook failure", site: wire.InitSnapshotSitePrime, outcome: wire.InitSnapshotOutcomeBeforeCheckpointFailed,
			snapshotErr: api.NewProblem(422, api.CodeBeforeCheckpointFailed, "callback failed", "private callback body")},
		{name: "park capture", site: wire.InitSnapshotSitePark, outcome: wire.InitSnapshotOutcomeCaptured},
		{name: "park snapshot failure", site: wire.InitSnapshotSitePark, outcome: wire.InitSnapshotOutcomeSnapshotFailed,
			snapshotErr: errors.New("private storage path")},
		{name: "park reuse", site: wire.InitSnapshotSitePark, outcome: wire.InitSnapshotOutcomeReused, reuse: true},
		{name: "park reuse cleanup failure", site: wire.InitSnapshotSitePark, outcome: wire.InitSnapshotOutcomeReuseCleanupFailed,
			reuse: true, destroyFails: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			_, app, dep := seedApp(t, store, api.PlanPro, 256, 3)
			if tc.reuse {
				_, err := store.CreateSnapshot(ctx, state.Snapshot{
					DeploymentID: dep.ID, FCVersion: "1.10.0",
					StorageKey: state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierInit, "reusable"),
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			vmm := &fakeVMM{snapErr: tc.snapshotErr}
			if tc.destroyFails {
				vmm.destroyErr = errors.New("private cleanup path")
			}
			ops := wire.NewOpsMetrics("schedd")
			e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").WithOpsMetrics(ops)
			var err error
			if tc.site == wire.InitSnapshotSitePrime {
				err = e.Prime(ctx, app.ID, dep.ID)
			} else {
				var wake WakeResult
				wake, err = e.Wake(ctx, app.ID, "", "", "")
				if err == nil {
					err = e.Park(ctx, wake.InstanceID)
				}
			}
			wantErr := tc.snapshotErr != nil || tc.destroyFails
			if (err != nil) != wantErr {
				t.Fatalf("operation error = %v, want failure=%t", err, wantErr)
			}
			body := getMetricsBody(t, ops)
			want := `schedd_init_snapshot_attempt_total{outcome="` + tc.outcome + `",site="` + tc.site + `"} 1`
			if !strings.Contains(body, want) {
				t.Fatalf("missing %q in metrics", want)
			}
			wantDuration := `schedd_init_snapshot_capture_duration_seconds_count{outcome="` + tc.outcome + `",site="` + tc.site + `"} 1`
			if tc.reuse {
				if strings.Contains(body, `schedd_init_snapshot_capture_duration_seconds_count{outcome="`+tc.outcome+`"`) {
					t.Fatal("reused snapshot entered capture duration histogram")
				}
			} else if !strings.Contains(body, wantDuration) {
				t.Fatalf("missing %q in metrics", wantDuration)
			}
		})
	}
}
