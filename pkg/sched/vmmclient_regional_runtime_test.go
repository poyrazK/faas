package sched_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/sched"
)

func TestVMMClientRegionalRuntimePinsSurviveWire(t *testing.T) {
	policy, _ := api.RuntimeQuotaPolicyForPlan(api.PlanPro)
	g := runtimequota.Grant{IssuerID: uuid.NewString(), GlobalAppID: uuid.NewString(), OwnerID: uuid.NewString(), AllocationID: uuid.NewString(), Generation: 1, IssuedAtMicros: 1, Policy: policy, Concurrency: 2,
		Binding: runtimequota.Binding{CellID: uuid.NewString(), Region: "eu-fsn", CellGeneration: 1, RegionalAccountID: uuid.NewString(), RegionalAppID: uuid.NewString(), RegionalAppSlug: "wire-test", APIBase: "https://cell.example.com"}}
	p := runtimequota.CapacityPermit{Grant: g, ID: uuid.NewString(), InstanceID: uuid.NewString(), RunID: uuid.NewString(), DeploymentID: uuid.NewString(), NodeID: uuid.NewString(), HostEpochID: uuid.NewString(), Kind: runtimequota.CapacityWarm}
	placement := &runtimequota.Placement{Permit: p}
	app := sched.AppSpec{RegionalRuntime: placement, BaseKey: "/b", LayerKey: "/l", VCPUCount: 2, MemSizeMiB: 256, AppID: g.Binding.RegionalAppID, AccountID: g.Binding.RegionalAccountID, DeploymentID: p.DeploymentID, Plan: api.PlanPro}
	for _, operation := range []string{"cold", "restore", "paused", "app-task"} {
		t.Run(operation, func(t *testing.T) {
			var received *runtimequota.Placement
			fake := &fakeVMM{}
			fake.wakeFn = func(ctx context.Context, req fcvm.WakeRequest) (*fcvm.Instance, error) {
				received = req.RegionalRuntime
				return (&fakeVMM{}).Wake(ctx, req)
			}
			c := newClient(t, fake)
			var err error
			switch operation {
			case "cold":
				_, err = c.CreateColdBoot(t.Context(), p.InstanceID, app)
			case "restore":
				_, err = c.CreateFromSnapshot(t.Context(), p.InstanceID, app, sched.SnapshotRef{})
			case "paused":
				_, err = c.CreatePausedFromSnapshot(t.Context(), p.InstanceID, app, sched.SnapshotRef{})
			case "app-task":
				_, err = c.RestoreAppTask(t.Context(), sched.AppTaskRestoreSpec{Instance: p.InstanceID, DeploymentID: p.DeploymentID, App: app})
				received = fake.appTaskWake.RegionalRuntime
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(received, placement) {
				t.Fatalf("wire lost original pins: %+v", received)
			}
		})
	}
	invalid := app
	invalid.RegionalRuntime = &runtimequota.Placement{}
	c := newClient(t, &fakeVMM{})
	if _, err := c.CreateColdBoot(t.Context(), p.InstanceID, invalid); err == nil {
		t.Fatal("invalid bookkeeping silently dropped")
	}
}
