// Hunt 32 H32-49 — park asks the guest for the before_checkpoint hook its
// deployment was built with, not the hook in the app's current manifest.
package sched_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

type hookRecordingVMM struct {
	*sched.FakeVMM
	beforeCheckpoint []bool
}

func (v *hookRecordingVMM) PauseAndSnapshot(ctx context.Context, node, instance, hostPath, memKey, stateKey string, beforeCheckpoint bool) (sched.SnapshotBytes, error) {
	v.beforeCheckpoint = append(v.beforeCheckpoint, beforeCheckpoint)
	return v.FakeVMM.PauseAndSnapshot(ctx, node, instance, hostPath, memKey, stateKey, beforeCheckpoint)
}

// A release built before the app added (or after it removed) a
// before_checkpoint hook has no matching guest config; asking that guest for
// the callback fails every park, prime and rollback to it.
func TestParkUsesTheDeploymentsBuiltBeforeCheckpointHook(t *testing.T) {
	hook := &api.BeforeCheckpointHook{Path: "/checkpoint"}
	tests := []struct {
		name    string
		appHook *api.BeforeCheckpointHook
		built   *bool
		want    bool
	}{
		{"built without the hook the app has now", hook, boolPtr(false), false},
		{"built with a hook the app has since dropped", nil, boolPtr(true), true},
		{"built with the hook", hook, boolPtr(true), true},
		{"built before the column existed falls back to the app", hook, nil, true},
		{"no hook anywhere", nil, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			acct, err := store.CreateAccount(ctx, "u@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{
				AccountID: acct.ID, Slug: "h32-49", RAMMB: 256, MaxConcurrency: 3, IdleTimeoutS: 60,
				Manifest: state.AppManifest{BeforeCheckpoint: tt.appHook},
			})
			if err != nil {
				t.Fatal(err)
			}
			dep, err := store.CreateDeployment(ctx, state.Deployment{
				AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc", Status: state.DeployLive,
			})
			if err != nil {
				t.Fatal(err)
			}
			if tt.built != nil {
				if err := store.SetDeploymentBeforeCheckpointHook(ctx, dep.ID, *tt.built); err != nil {
					t.Fatal(err)
				}
			}
			vmm := &hookRecordingVMM{FakeVMM: &sched.FakeVMM{}}
			e := sched.XNewEngine(t, store, vmm, &sched.FakeNotifier{}, "1.10.0")
			res, err := e.Wake(ctx, app.ID, "", "", "")
			if err != nil {
				t.Fatal(err)
			}
			if err := e.Park(ctx, res.InstanceID); err != nil {
				t.Fatalf("Park: %v", err)
			}
			if len(vmm.beforeCheckpoint) != 1 || vmm.beforeCheckpoint[0] != tt.want {
				t.Fatalf("PauseAndSnapshot beforeCheckpoint = %v, want [%t]", vmm.beforeCheckpoint, tt.want)
			}
		})
	}
}

func boolPtr(v bool) *bool { return &v }
