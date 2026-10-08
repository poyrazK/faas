package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
)

type devPatchFixture struct {
	store    *state.MemStore
	app      state.App
	dep      state.Deployment
	receiver *runtimeConfigReceiver
	diverged *vmmdgrpc.DivergedInstances
}

func newRuntimeDevPatchFixture(t *testing.T, developer, enabled bool) devPatchFixture {
	t.Helper()
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "dev-patch@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app := state.App{AccountID: acct.ID, Slug: "dev-api-0123456789ab", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 60}
	if developer {
		app.PreviewOfSlug, app.PreviewPrNumber, app.PreviewPrState = "api", 0, state.PreviewPrStateOpen
	}
	app, err = store.CreateApp(ctx, app)
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, Status: state.DeployLive,
		ImageDigest: "sha256:" + hex.EncodeToString(make([]byte, 32)), CreatedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	manager := fcvm.NewManager(nil, nil, fcvm.Paths{}, "test", nil, nil).RegisterInstanceForTest("instance-1", dep.ID, app.ID, acct.ID)
	diverged := vmmdgrpc.NewDivergedInstances()
	receiver := &runtimeConfigReceiver{ctx: ctx, mgr: manager, store: store, diverged: diverged, devPatchEnabled: enabled}
	return devPatchFixture{store: store, app: app, dep: dep, receiver: receiver, diverged: diverged}
}

func (f devPatchFixture) publish(t *testing.T, archive string, corruptDigest bool) {
	t.Helper()
	sum := sha256.Sum256([]byte(archive))
	digest := hex.EncodeToString(sum[:])
	if corruptDigest {
		digest = hex.EncodeToString(make([]byte, 32))
	}
	if _, err := f.store.CreateDevSourcePatch(context.Background(), state.DevSourcePatch{AppID: f.app.ID, BaseDeploymentID: f.dep.ID,
		ImageDir: "/app", Archive: []byte(archive), Deleted: []string{"src/old.js"}, Digest: digest, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeDevPatchServesNewestPatchAndMarksInstance(t *testing.T) {
	f := newRuntimeDevPatchFixture(t, true, true)
	response := sendRuntimeConfigTestRequest(t, f.receiver, runtimeConfigRequest{Kind: runtimeDevPatchKind})
	if !response.Unchanged || response.DevPatch != nil || f.diverged.Has("instance-1") {
		t.Fatalf("poll without a patch = %+v (marked=%t), want unchanged and unmarked", response, f.diverged.Has("instance-1"))
	}
	f.publish(t, "first", false)
	f.publish(t, "second", false)
	response = sendRuntimeConfigTestRequest(t, f.receiver, runtimeConfigRequest{Kind: runtimeDevPatchKind})
	if response.DevPatch == nil || response.DevPatch.Generation != 2 || string(response.DevPatch.Archive) != "second" ||
		response.DevPatch.ImageDir != "/app" || len(response.DevPatch.Deleted) != 1 {
		t.Fatalf("patch response = %+v", response)
	}
	if !f.diverged.Has("instance-1") {
		t.Fatal("serving a patch did not mark the instance as diverged")
	}
	response = sendRuntimeConfigTestRequest(t, f.receiver, runtimeConfigRequest{Kind: runtimeDevPatchKind, PatchGeneration: 2})
	if !response.Unchanged {
		t.Fatalf("poll at the newest generation = %+v, want unchanged", response)
	}
}

func TestRuntimeDevPatchRefusals(t *testing.T) {
	cases := []struct {
		name      string
		developer bool
		enabled   bool
		corrupt   bool
		request   runtimeConfigRequest
		wantError string
	}{
		{name: "delivery flag off", developer: true, enabled: false, request: runtimeConfigRequest{Kind: runtimeDevPatchKind}, wantError: runtimeDevPatchDisabled},
		{name: "not a developer app", developer: false, enabled: true, request: runtimeConfigRequest{Kind: runtimeDevPatchKind}, wantError: runtimeDevPatchDisabled},
		{name: "digest mismatch", developer: true, enabled: true, corrupt: true, request: runtimeConfigRequest{Kind: runtimeDevPatchKind}, wantError: runtimeDevPatchUnavailable},
		{name: "negative generation", developer: true, enabled: true, request: runtimeConfigRequest{Kind: runtimeDevPatchKind, PatchGeneration: -1}, wantError: "invalid_request"},
		{name: "patch generation on another kind", developer: true, enabled: true, request: runtimeConfigRequest{Kind: "env", PatchGeneration: 1}, wantError: "invalid_request"},
		{name: "secret fields on a patch poll", developer: true, enabled: true, request: runtimeConfigRequest{Kind: runtimeDevPatchKind, Revision: "x"}, wantError: "invalid_request"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newRuntimeDevPatchFixture(t, tc.developer, tc.enabled)
			f.publish(t, "patch", tc.corrupt)
			response := sendRuntimeConfigTestRequest(t, f.receiver, tc.request)
			if response.Error != tc.wantError || response.DevPatch != nil {
				t.Fatalf("response = %+v, want error %q", response, tc.wantError)
			}
			if f.diverged.Has("instance-1") {
				t.Fatal("a refused poll marked the instance as diverged")
			}
		})
	}
}
