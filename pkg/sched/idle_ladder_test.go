// adr: 20261107122928 — schedd marks every request-serving instance for the
// idle ladder's observe mode, whatever the app's declared pause.
package sched_test

import (
	"context"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAppSpecToProtoCarriesIdleLadderEligible(t *testing.T) {
	if !(sched.AppSpec{IdleLadderEligible: true}).ToProto().GetIdleLadderEligible() {
		t.Fatal("idle_ladder_eligible dropped on the wire")
	}
}

func TestWakeMarksRequestServingInstancesForTheIdleLadder(t *testing.T) {
	for _, tc := range []struct {
		name  string
		typ   state.AppType
		class state.WorkloadClass
		want  bool
	}{
		{name: "function", typ: state.AppTypeFunction, want: true},
		{name: "http app with the default pause", typ: "", class: state.WorkloadClassHTTP, want: true},
		{name: "worker", typ: "", class: state.WorkloadClassWorker, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			acct, err := store.CreateAccount(ctx, "u@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "ladder", Type: tc.typ,
				WorkloadClass: tc.class, RAMMB: 256, MaxConcurrency: 4, IdleTimeoutS: 60})
			if err != nil {
				t.Fatal(err)
			}
			dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage,
				ImageDigest: "sha256:ladder", Status: state.DeployLive})
			if err != nil {
				t.Fatal(err)
			}
			vmm := &sched.FakeVMM{}
			engine := sched.XNewEngine(t, store, vmm, &sched.FakeNotifier{}, "1.10.0")
			if _, err := engine.Wake(ctx, app.ID, dep.ID, "", ""); err != nil {
				t.Fatalf("Wake: %v", err)
			}
			if got := (*vmm.LastColdBootSpec()).IdleLadderEligible; got != tc.want {
				t.Fatalf("cold boot idle_ladder_eligible = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPauseTierWorkloadExcludesNonServingInstances(t *testing.T) {
	http := state.App{WorkloadClass: state.WorkloadClassHTTP}
	for _, tc := range []struct {
		name    string
		app     state.App
		mode    string
		network string
		want    bool
	}{
		{name: "http app", app: http, mode: string(state.InstanceModeNormal), want: true},
		{name: "legacy empty class and mode", app: state.App{}, want: true},
		{name: "grpc app", app: state.App{WorkloadClass: state.WorkloadClassGRPC}, want: true},
		{name: "worker class", app: state.App{WorkloadClass: state.WorkloadClassWorker}},
		{name: "mirror instance", app: http, mode: string(state.InstanceModeMirror)},
		{name: "private network peer", app: http, network: "net-1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sched.PauseTierWorkload(tc.app, tc.mode, tc.network); got != tc.want {
				t.Fatalf("pauseTierWorkload = %v, want %v", got, tc.want)
			}
		})
	}
}

// adr: 20261107122928 — a fleet poll of vmmd's Stats carries the idle ladder
// level of each instance.
func TestVMInstanceStatFromProtoCarriesIdleLadderLevel(t *testing.T) {
	row := sched.VMInstanceStatFromProto(&vmmdpb.InstanceStats{Instance: "vm-a", QuietPaused: true, IdleLadderLevel: 2})
	if row.InstanceID != "vm-a" || !row.QuietPaused || row.IdleLadderLevel != 2 {
		t.Fatalf("row = %+v, want vm-a quiet-paused at level 2", row)
	}
}
