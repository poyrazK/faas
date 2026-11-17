// adr: 038 (amendment 2026-11-17) — the schedd that owns an app verifies a
// deployment's layer when imaged announces it, so the prime does not.
package sched_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

type recordingLayerVerifier struct {
	mu    sync.Mutex
	calls []string
	err   error
}

func (v *recordingLayerVerifier) Verify(_ context.Context, layer, sig string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.calls = append(v.calls, layer+" "+sig)
	return v.err
}

func (v *recordingLayerVerifier) seen() []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return append([]string(nil), v.calls...)
}

func preverifyDeployment(ctx context.Context, t *testing.T, store *state.MemStore, appID string, status state.DeploymentStatus, key string) state.Deployment {
	t.Helper()
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: appID, Kind: state.DeploymentKindDockerfile, Status: status})
	if err != nil {
		t.Fatal(err)
	}
	if key != "" {
		if err := store.SetDeploymentRootfs(ctx, dep.ID, "/srv/apps/"+dep.ID+".ext4", key, 1); err != nil {
			t.Fatal(err)
		}
	}
	return dep
}

func TestLayerPublishedVerifiesTheLayerTheOwnersPrimeBoots(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	_, app, _ := sched.SeedApp(t, store, api.PlanPro, 512, 5)
	if err := store.SetAppNodeID(ctx, app.ID, "node-a"); err != nil {
		t.Fatal(err)
	}
	dep := preverifyDeployment(ctx, t, store, app.ID, state.DeployImaging, "apps/preverify/layer.ext4")
	verifier := &recordingLayerVerifier{}
	engine := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0").
		WithOwnerNodeID("node-a").WithVerifier(verifier)
	loop := sched.NewLoop(nil, engine, sched.TestLog())

	loop.XHandleNotification(ctx, db.Notification{
		Channel: db.NotifyLayerPublished,
		Payload: `{"app_id":"` + app.ID + `","deployment_id":"` + dep.ID + `"}`,
	})
	loop.XHandleNotification(ctx, db.Notification{Channel: db.NotifyLayerPublished, Payload: `{"app_id":""}`})
	loop.WorkPool().Drain()

	want := "apps/preverify/layer.ext4 sigs/apps/preverify/layer.ext4.sig"
	if got := verifier.seen(); len(got) != 1 || got[0] != want {
		t.Fatalf("verified %q, want only %q", got, want)
	}
}

func TestPreverifyPrimeLayerLeavesOtherLayersAlone(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	acct, app, _ := sched.SeedApp(t, store, api.PlanPro, 512, 5)
	peer, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "peer", RAMMB: 512, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetAppNodeID(ctx, app.ID, "node-a"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetAppNodeID(ctx, peer.ID, "node-b"); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		appID string
		dep   state.Deployment
	}{
		{"another node's app", peer.ID, preverifyDeployment(ctx, t, store, peer.ID, state.DeployImaging, "apps/peer.ext4")},
		{"live deployment", app.ID, preverifyDeployment(ctx, t, store, app.ID, state.DeployLive, "apps/live.ext4")},
		{"failed deployment", app.ID, preverifyDeployment(ctx, t, store, app.ID, state.DeployFailed, "apps/failed.ext4")},
		{"no layer yet", app.ID, preverifyDeployment(ctx, t, store, app.ID, state.DeployImaging, "")},
		{"another app's deployment", app.ID, preverifyDeployment(ctx, t, store, peer.ID, state.DeployImaging, "apps/other.ext4")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			verifier := &recordingLayerVerifier{}
			engine := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0").
				WithOwnerNodeID("node-a").WithVerifier(verifier)
			if err := engine.PreverifyPrimeLayer(ctx, tc.appID, tc.dep.ID); err != nil {
				t.Fatal(err)
			}
			if got := verifier.seen(); len(got) != 0 {
				t.Fatalf("verified %q, want nothing", got)
			}
		})
	}

	t.Run("a failed verification is returned", func(t *testing.T) {
		refused := errors.New("signature does not match ext4")
		engine := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0").
			WithOwnerNodeID("node-a").WithVerifier(&recordingLayerVerifier{err: refused})
		dep := preverifyDeployment(ctx, t, store, app.ID, state.DeploySnapshotting, "apps/tampered.ext4")
		if err := engine.PreverifyPrimeLayer(ctx, app.ID, dep.ID); !errors.Is(err, refused) {
			t.Fatalf("err = %v, want the verifier's refusal", err)
		}
	})
}
