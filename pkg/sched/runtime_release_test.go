package sched

// adr: 736

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type runtimeReleaseLookupFailure struct {
	state.Store
	state.RuntimeReleaseStore
}

func (s runtimeReleaseLookupFailure) RuntimeReleaseForArtifact(context.Context, string, string) (state.RuntimeRelease, error) {
	return state.RuntimeRelease{}, errors.New("database unavailable")
}
func TestRuntimeReleasePrimeWakeAndLookupFailure(t *testing.T) {
	ctx := t.Context()
	s := state.NewMemStore()
	acct, err := s.CreateAccount(ctx, "runtime-prime@test.example", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "runtime-prime", Type: state.AppTypeFunction, Runtime: "node22", RAMMB: 256, MaxConcurrency: 2, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, ImageDigest: "sha256:app", Status: state.DeployImaging})
	if err != nil {
		t.Fatal(err)
	}
	key := "apps/runtime-prime/layer.ext4"
	if err := s.SetDeploymentRootfs(ctx, dep.ID, "/test/layer", key, 10); err != nil {
		t.Fatal(err)
	}
	r := state.RuntimeRelease{Runtime: "node22", Architecture: runtime.GOARCH, SourceRef: "ghcr.io/test/base@sha256:" + strings.Repeat("a", 64), GuestInitSHA256: strings.Repeat("b", 64), LayoutVersion: "test-layout", BaseSHA256: strings.Repeat("c", 64)}
	r.ID = r.Identity()
	pinned, err := s.PublishRuntimeRelease(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.BindDeploymentRuntimeRelease(ctx, dep.ID, key, pinned.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateDeploymentStatus(ctx, dep.ID, state.DeployLive, ""); err != nil {
		t.Fatal(err)
	}
	vmm := &fakeVMM{}
	e := newEngine(t, s, vmm, &fakeNotifier{}, "1.10.0")
	if err := e.Prime(ctx, app.ID, dep.ID); err != nil {
		t.Fatal(err)
	}
	if vmm.lastColdBootSpec.BaseKey != pinned.BaseKey() {
		t.Fatal("prime used mutable base", vmm.lastColdBootSpec.BaseKey)
	}
	// A newer published default never changes an older artifact's cold boot.
	r.SourceRef = "ghcr.io/test/base@sha256:" + strings.Repeat("d", 64)
	r.ID = r.Identity()
	if _, err := s.PublishRuntimeRelease(ctx, r); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Wake(ctx, app.ID, "", "", ""); err != nil {
		t.Fatal(err)
	}
	if vmm.lastColdBootSpec.BaseKey != pinned.BaseKey() {
		t.Fatal("wake changed runtime", vmm.lastColdBootSpec.BaseKey)
	}
	instances, err := s.ListInstancesForApp(ctx, app.ID)
	if err != nil || len(instances) == 0 {
		t.Fatal("missing wake instance", instances, err)
	}
	migration, _, err := e.buildAppSpecForMigrationWithValues(ctx, instances[0].ID)
	if err != nil || migration.BaseKey != pinned.BaseKey() {
		t.Fatal("migration changed runtime", migration.BaseKey, err)
	}
	failed := &Engine{store: runtimeReleaseLookupFailure{Store: s, RuntimeReleaseStore: s}}
	if key, err := failed.artifactBaseKey(ctx, app, "apps/runtime-prime/layer.ext4"); err == nil || key != "" {
		t.Fatal("lookup error used mutable fallback", key, err)
	}
	if key, err := e.artifactBaseKey(ctx, app, "legacy/layer.ext4"); err != nil || key != BaseKey(app.Runtime) {
		t.Fatal("legacy behavior changed", key, err)
	}
}
