package sched_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/wire"
)

func TestVMMClientRegionalRuntimePinsSurviveWire(t *testing.T) {
	policy, _ := api.RuntimeQuotaPolicyForPlan(api.PlanPro)
	g := runtimequota.Grant{IssuerID: uuid.NewString(), GlobalAppID: uuid.NewString(), OwnerID: uuid.NewString(), AllocationID: uuid.NewString(), Generation: 1, IssuedAtMicros: 1, Policy: policy, Concurrency: 2,
		Binding: runtimequota.Binding{CellID: uuid.NewString(), Region: "eu-fsn", CellGeneration: 1, RegionalAccountID: uuid.NewString(), RegionalAppID: uuid.NewString(), RegionalAppSlug: "wire-test", APIBase: "https://cell.example.com"}}
	p := runtimequota.CapacityPermit{Grant: g, ID: uuid.NewString(), InstanceID: uuid.NewString(), RunID: uuid.NewString(), DeploymentID: uuid.NewString(), NodeID: uuid.NewString(), HostEpochID: uuid.NewString(), Kind: runtimequota.CapacityWarm}
	placement := &runtimequota.Placement{Permit: p}
	app := sched.AppSpec{RegionalRuntime: placement, BaseKey: "/b", LayerKey: "/l", VCPUCount: 2, MemSizeMiB: 256, AppID: g.Binding.RegionalAppID, AccountID: g.Binding.RegionalAccountID, DeploymentID: p.DeploymentID, Plan: api.PlanPro}
	for _, operation := range []string{"cold", "restore", "paused", "app-task", "prime"} {
		t.Run(operation, func(t *testing.T) {
			p := p
			if operation == "app-task" {
				p.Kind = runtimequota.CapacityAppTask
			}
			if operation == "prime" {
				p.Kind = runtimequota.CapacityPrime
			}
			placement := &runtimequota.Placement{Permit: p}
			if operation == "cold" || operation == "restore" {
				placement.Seat = &runtimequota.ServingSeat{Permit: p, ID: uuid.NewString()}
			}
			app := app
			app.RegionalRuntime = placement
			app.PrimeOnly = operation == "prime"
			var received *runtimequota.Placement
			var receivedRequest fcvm.WakeRequest
			fake := &fakeVMM{}
			fake.wakeFn = func(ctx context.Context, req fcvm.WakeRequest) (*fcvm.Instance, error) {
				received = req.RegionalRuntime
				receivedRequest = req
				return (&fakeVMM{}).Wake(ctx, req)
			}
			c := newClient(t, fake)
			var err error
			switch operation {
			case "cold", "prime":
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
			if operation != "app-task" && (receivedRequest.DeploymentID != p.DeploymentID || receivedRequest.Plan != g.Policy.Plan || receivedRequest.PrimeOnly != app.PrimeOnly) {
				t.Fatalf("wire lost actual lifecycle binding: %+v", receivedRequest)
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

func TestVMMClientRegionalBindingRejectsBeforeRPC(t *testing.T) {
	policy, _ := api.RuntimeQuotaPolicyForPlan(api.PlanPro)
	g := runtimequota.Grant{IssuerID: uuid.NewString(), GlobalAppID: uuid.NewString(), OwnerID: uuid.NewString(), AllocationID: uuid.NewString(), Generation: 1, IssuedAtMicros: 1, Policy: policy, Concurrency: 2,
		Binding: runtimequota.Binding{CellID: uuid.NewString(), Region: "eu-fsn", CellGeneration: 1, RegionalAccountID: uuid.NewString(), RegionalAppID: uuid.NewString(), RegionalAppSlug: "wire-test", APIBase: "https://cell.example.com"}}
	base := runtimequota.CapacityPermit{Grant: g, ID: uuid.NewString(), InstanceID: uuid.NewString(), RunID: uuid.NewString(), DeploymentID: uuid.NewString(), NodeID: uuid.NewString(), HostEpochID: uuid.NewString(), Kind: runtimequota.CapacityServing}
	for _, operation := range []string{"wrong-plan", "missing-deployment", "conflicting-correlation", "prime-as-serving", "serving-as-prime", "paused-with-seat", "prime-restore", "prime-migration", "prime-task", "serving-as-task"} {
		t.Run(operation, func(t *testing.T) {
			p := base
			placement := &runtimequota.Placement{Permit: p, Seat: &runtimequota.ServingSeat{Permit: p, ID: uuid.NewString()}}
			app := sched.AppSpec{RegionalRuntime: placement, Plan: api.PlanPro, AppID: g.Binding.RegionalAppID, AccountID: g.Binding.RegionalAccountID, DeploymentID: p.DeploymentID}
			ctx := t.Context()
			switch operation {
			case "wrong-plan":
				app.Plan = api.PlanScale
			case "missing-deployment":
				app.DeploymentID = ""
			case "conflicting-correlation":
				ctx = wire.WithContext(ctx, wire.CorrelationFields{DeploymentID: uuid.NewString()})
			case "prime-as-serving":
				app.RegionalRuntime.Permit.Kind = runtimequota.CapacityPrime
				app.RegionalRuntime.Seat = nil
			case "serving-as-prime", "prime-restore", "prime-migration", "prime-task":
				app.PrimeOnly = true
			}
			// A nil client cannot perform an RPC. Binding errors must be
			// returned before dereferencing its transport.
			var c *sched.VMMClient
			var err error
			switch operation {
			case "paused-with-seat":
				_, err = c.CreatePausedFromSnapshot(ctx, p.InstanceID, app, sched.SnapshotRef{})
			case "prime-restore":
				_, err = c.CreateFromSnapshot(ctx, p.InstanceID, app, sched.SnapshotRef{})
			case "prime-migration":
				_, err = c.AdoptMigratedInstance(ctx, "node", p.InstanceID, app, "mem", "state", "lease")
			case "prime-task", "serving-as-task":
				_, err = c.RestoreAppTask(ctx, sched.AppTaskRestoreSpec{Instance: p.InstanceID, DeploymentID: p.DeploymentID, App: app})
			default:
				_, err = c.CreateColdBoot(ctx, p.InstanceID, app)
			}
			if !errors.Is(err, runtimequota.ErrBinding) {
				t.Fatalf("unbound operation reached transport: %v", err)
			}
		})
	}
}
