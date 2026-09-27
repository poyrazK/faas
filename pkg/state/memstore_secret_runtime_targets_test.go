package state_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemStoreAppSecretRuntimeReloadTargetsIncludesUnknownAndUnreported(t *testing.T) {
	store, ctx, account, app := memValueHashFixture(t)
	makeDeployment := func(scope string, override, sidecars json.RawMessage) state.Deployment {
		t.Helper()
		deployment, err := store.CreateDeployment(ctx, state.Deployment{
			AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + scope,
			Status: state.DeployLive, Scope: scope, OverrideEnvSecrets: override, Sidecars: sidecars,
		})
		if err != nil {
			t.Fatalf("create %s deployment: %v", scope, err)
		}
		return deployment
	}
	makeRuntime := func(deployment state.Deployment) state.Instance {
		t.Helper()
		instance, err := store.CreateInstance(ctx, app.ID, deployment.ID, string(state.StateRunning), 256, "node-1", "")
		if err != nil {
			t.Fatalf("create %s runtime: %v", deployment.Scope, err)
		}
		return instance
	}
	upsert := func(scope, key string) {
		t.Helper()
		if err := store.UpsertAppSecretWithKidAndValueHashInScope(ctx, account.ID, app.ID, scope, key, "kid-1", "1111111111111111", []byte("cipher")); err != nil {
			t.Fatalf("seed %s/%s secret: %v", scope, key, err)
		}
	}

	prod := makeDeployment("prod", json.RawMessage(`{"DATABASE_URL":"vault://prod/db"}`), nil)
	if err := store.SetDeploymentSecretReloadSignal(ctx, prod.ID, "SIGHUP"); err != nil {
		t.Fatalf("enable prod reload: %v", err)
	}
	prodRuntime := makeRuntime(prod)
	upsert("prod", "DATABASE_URL")
	upsert("prod", "UNAUTHORIZED_TOKEN")

	staging := makeDeployment("staging", nil, nil)
	stagingRuntime := makeRuntime(staging) // A pre-migration deployment is unknown, not disabled.
	upsert("staging", "STAGING_TOKEN")

	sidecar := makeDeployment("sidecar", json.RawMessage(`{"MAIN_TOKEN":"secret:MAIN_TOKEN"}`), json.RawMessage(`[{"name":"worker","type":"sidecar","env_secrets":{"SIDECAR_TOKEN":"secret:SIDECAR_TOKEN"}}]`))
	if err := store.SetDeploymentSecretReloadSignal(ctx, sidecar.ID, ""); err != nil {
		t.Fatalf("disable sidecar reload: %v", err)
	}
	sidecarRuntime := makeRuntime(sidecar)
	upsert("sidecar", "MAIN_TOKEN")
	upsert("sidecar", "SIDECAR_TOKEN")
	upsert("sidecar", "UNAUTHORIZED_SIDE_TOKEN")

	dev := makeDeployment("dev", nil, nil)
	if err := store.SetDeploymentSecretReloadSignal(ctx, dev.ID, ""); err != nil {
		t.Fatalf("disable dev reload: %v", err)
	}
	devRuntime := makeRuntime(dev)
	upsert("dev", "DEV_TOKEN")

	if _, err := store.RecordAppSecretRuntimeReload(ctx, state.AppSecretRuntimeReloadResult{
		AccountID: account.ID, AppID: app.ID, InstanceID: prodRuntime.ID, Revision: strings.Repeat("a", 64),
		Projection: state.SecretReloadProjectionUpdated, Signal: state.SecretReloadSignalSent,
		AttemptedAt: time.Now().UTC(), Candidates: []state.AppSecretDeliveryCandidate{{Scope: "prod", Key: "DATABASE_URL", Version: 1}},
	}); err != nil {
		t.Fatalf("record prod report: %v", err)
	}
	targets, err := store.ListAppSecretRuntimeReloadTargets(ctx, account.ID, app.ID, "")
	if err != nil {
		t.Fatalf("list active targets: %v", err)
	}
	if len(targets) != 5 {
		t.Fatalf("targets = %+v, want prod, legacy staging, sidecar main+grant, and dev targets", targets)
	}
	byRuntimeKey := make(map[string]state.AppSecretRuntimeReloadTarget, len(targets))
	for _, target := range targets {
		byRuntimeKey[target.InstanceID+"\x00"+target.Key] = target
	}
	if got := byRuntimeKey[prodRuntime.ID+"\x00DATABASE_URL"]; got.ReloadSupport != "enabled" || !got.Reported || got.Version != 1 {
		t.Errorf("reported opt-in target = %+v, want enabled/reported/v1", got)
	}
	if got := byRuntimeKey[stagingRuntime.ID+"\x00STAGING_TOKEN"]; got.ReloadSupport != "unknown" || got.Reported {
		t.Errorf("legacy unreported target = %+v, want unknown/unreported", got)
	}
	if got := byRuntimeKey[devRuntime.ID+"\x00DEV_TOKEN"]; got.ReloadSupport != "disabled" || got.Reported {
		t.Errorf("explicit opt-out target = %+v, want disabled/unreported", got)
	}
	for _, target := range targets {
		if target.Key == "UNAUTHORIZED_TOKEN" || target.Key == "UNAUTHORIZED_SIDE_TOKEN" {
			t.Errorf("deployment override allowlist leaked an unauthorized target: %+v", target)
		}
	}
	if got := byRuntimeKey[sidecarRuntime.ID+"\x00SIDECAR_TOKEN"]; got.ReloadSupport != "disabled" {
		t.Errorf("sidecar secret target = %+v, want SIDECAR_TOKEN with reload disabled", got)
	}
}
