// adr: 590
package sched

import (
	"context"
	"errors"
	"testing"
)

type cloneWakePausedVMM struct{ fakeRouterVMM }

func (f *cloneWakePausedVMM) CreatePausedFromSnapshot(ctx context.Context, instance string, app AppSpec, snap SnapshotRef) (*WakeOutcome, error) {
	return f.CreateFromSnapshot(ctx, instance, app, snap)
}

func TestVMMRouterCloneWakeDispatchBeforeRPC(t *testing.T) {
	for _, method := range []string{"cold_boot", "restore", "paused_restore"} {
		t.Run(method, func(t *testing.T) {
			cli := &cloneWakePausedVMM{}
			r := NewVMMRouter(nil, nil, nil)
			r.SetClient("node", cli)
			denied := errors.New("retirement fenced")
			allow := false
			r.WithCloneWakeDispatchRecorder(func(_ context.Context, instance, app, dep, node, recordedMethod string) error {
				if instance != "instance" || app != "app" || dep != "deployment" || node != "node" {
					t.Fatal("changed dispatch identity")
				}
				want := "restore"
				if method == "cold_boot" {
					want = "cold_boot"
				}
				if recordedMethod != want {
					t.Fatal("wrong method")
				}
				if len(cli.instanceCalls) != 0 {
					t.Fatal("RPC before durable record")
				}
				if !allow {
					return denied
				}
				return nil
			})
			call := func() error {
				app := AppSpec{AppID: "app", DeploymentID: "deployment"}
				snap := SnapshotRef{DeploymentID: "deployment"}
				var err error
				switch method {
				case "cold_boot":
					_, err = r.CreateColdBoot(t.Context(), "node", "instance", app)
				case "restore":
					_, err = r.CreateFromSnapshot(t.Context(), "node", "instance", app, snap)
				default:
					_, err = r.CreatePausedFromSnapshot(t.Context(), "node", "instance", app, snap)
				}
				return err
			}
			if err := call(); !errors.Is(err, denied) || len(cli.instanceCalls) != 0 {
				t.Fatal("fenced RPC sent", err)
			}
			allow = true
			if err := call(); err != nil || len(cli.instanceCalls) != 1 {
				t.Fatal("admitted RPC not sent", err)
			}
		})
	}
}
