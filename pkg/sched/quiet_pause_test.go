// adr: 1151 — schedd asks vmmd to quiet-pause only function apps that serve
// HTTP, woken as ordinary instances, outside private networks.
package sched_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestQuietPauseAfterMS(t *testing.T) {
	function := state.App{Type: state.AppTypeFunction, WorkloadClass: state.WorkloadClassHTTP}
	for _, tc := range []struct {
		name    string
		app     state.App
		mode    string
		network string
		want    int64
	}{
		{name: "http function", app: function, mode: string(state.InstanceModeNormal), want: api.QuietPauseAfterMillis},
		{name: "function with legacy empty class and mode", app: state.App{Type: state.AppTypeFunction}, want: api.QuietPauseAfterMillis},
		{name: "service", app: state.App{WorkloadClass: state.WorkloadClassHTTP}, mode: string(state.InstanceModeNormal)},
		{name: "worker function", app: state.App{Type: state.AppTypeFunction, WorkloadClass: state.WorkloadClassWorker}},
		{name: "mirror instance", app: function, mode: string(state.InstanceModeMirror)},
		{name: "worker instance", app: function, mode: string(state.InstanceModeWorker)},
		{name: "private network peer", app: function, mode: string(state.InstanceModeNormal), network: "net-1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sched.QuietPauseAfterMS(tc.app, tc.mode, tc.network); got != tc.want {
				t.Fatalf("quietPauseAfterMS = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestAppSpecToProtoCarriesQuietPause(t *testing.T) {
	if got := (sched.AppSpec{QuietPauseAfterMS: 2000}).ToProto().GetQuietPauseAfterMs(); got != 2000 {
		t.Fatalf("quiet_pause_after_ms = %d, want 2000", got)
	}
}

func TestWakeAsksVMMDToQuietPauseFunctions(t *testing.T) {
	for _, tc := range []struct {
		name string
		typ  state.AppType
		want int64
	}{
		{name: "function", typ: state.AppTypeFunction, want: api.QuietPauseAfterMillis},
		{name: "service", typ: "", want: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			acct, err := store.CreateAccount(ctx, "u@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "quiet", Type: tc.typ,
				RAMMB: 256, MaxConcurrency: 4, IdleTimeoutS: 60})
			if err != nil {
				t.Fatal(err)
			}
			dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage,
				ImageDigest: "sha256:quiet", Status: state.DeployLive})
			if err != nil {
				t.Fatal(err)
			}
			vmm := &sched.FakeVMM{}
			engine := sched.XNewEngine(t, store, vmm, &sched.FakeNotifier{}, "1.10.0")
			if _, err := engine.Wake(ctx, app.ID, dep.ID, "", ""); err != nil {
				t.Fatalf("Wake: %v", err)
			}
			if got := (*vmm.LastColdBootSpec()).QuietPauseAfterMS; got != tc.want {
				t.Fatalf("cold boot quiet_pause_after_ms = %d, want %d", got, tc.want)
			}
		})
	}
}
