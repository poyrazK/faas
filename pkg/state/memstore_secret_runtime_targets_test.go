// adr: 375
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
	if err := store.SetDeploymentSidecarSecretReloadSignal(ctx, sidecar.ID, "worker", ""); err != nil {
		t.Fatalf("disable worker reload: %v", err)
	}
	sidecarRuntime := makeRuntime(sidecar)
	upsert("sidecar", "MAIN_TOKEN")
	upsert("sidecar", "SIDECAR_TOKEN")
	upsert("sidecar", "UNAUTHORIZED_SIDE_TOKEN")

	legacySidecar := makeDeployment("legacy-sidecar", nil, json.RawMessage(`[{"name":"worker","type":"sidecar","env_secrets":{"SIDE_ONLY":"secret:SIDE_ONLY"}}]`))
	legacySidecarRuntime := makeRuntime(legacySidecar)
	upsert("legacy-sidecar", "SIDE_ONLY")

	dev := makeDeployment("dev", nil, nil)
	if err := store.SetDeploymentSecretReloadSignal(ctx, dev.ID, ""); err != nil {
		t.Fatalf("disable dev reload: %v", err)
	}
	devRuntime := makeRuntime(dev)
	upsert("dev", "DEV_TOKEN")

	if _, err := store.RecordAppSecretRuntimeReload(ctx, state.AppSecretRuntimeReloadResult{
		AccountID: account.ID, AppID: app.ID, InstanceID: prodRuntime.ID, Revision: strings.Repeat("a", 64),
		Fence:      runtimeSecretFenceForTest(t, store, runtimeAppEnvFixture{account: account, app: app}, prod),
		Projection: state.SecretReloadProjectionUpdated, Signal: state.SecretReloadSignalSent,
		AttemptedAt: time.Now().UTC(), Candidates: []state.AppSecretDeliveryCandidate{{Scope: "prod", Key: "DATABASE_URL", Version: 1}},
	}); err != nil {
		t.Fatalf("record prod report: %v", err)
	}
	targets, err := store.ListAppSecretRuntimeReloadTargets(ctx, account.ID, app.ID, "")
	if err != nil {
		t.Fatalf("list active targets: %v", err)
	}
	if len(targets) != 6 {
		t.Fatalf("targets = %+v, want prod, legacy staging, sidecar main+grant, legacy sidecar grant, and dev targets", targets)
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
	for _, target := range targets {
		if target.InstanceID == legacySidecarRuntime.ID && target.Key == "SIDE_ONLY" && target.WorkloadName != "worker" {
			t.Errorf("legacy all-in-scope behavior leaked a sidecar-only secret into the main target: %+v", target)
		}
	}
}

func TestMemStoreSecretReloadObservationsArePerWorkload(t *testing.T) {
	store, ctx, account, app := memValueHashFixture(t)
	deployment, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:sidecar-reload",
		Status: state.DeployLive, Scope: "prod",
		OverrideEnvSecrets: json.RawMessage(`{"DATABASE_URL":"secret:DATABASE_URL"}`),
		Sidecars:           json.RawMessage(`[{"name":"proxy","type":"sidecar","env_secrets":{"DATABASE_URL":"secret:DATABASE_URL"}}]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentSecretReloadSignal(ctx, deployment.ID, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentSidecarSecretReloadSignal(ctx, deployment.ID, "proxy", "SIGHUP"); err != nil {
		t.Fatal(err)
	}
	instance, err := store.CreateInstance(ctx, app.ID, deployment.ID, string(state.StateRunning), 256, "node-1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAppSecretWithKidAndValueHashInScope(ctx, account.ID, app.ID, "prod", "DATABASE_URL", "kid-1", "1111111111111111", []byte("cipher")); err != nil {
		t.Fatal(err)
	}
	secrets, err := store.ListAppSecretsInScope(ctx, account.ID, app.ID, "prod")
	if err != nil || len(secrets) != 1 {
		t.Fatalf("list seeded secret = %+v, err=%v", secrets, err)
	}
	candidate := state.AppSecretDeliveryCandidate{Scope: "prod", Key: "DATABASE_URL", Version: secrets[0].DeliveryVersion}
	fence := runtimeSecretFenceForTest(t, store, runtimeAppEnvFixture{account: account, app: app}, deployment)
	for _, workloadName := range []string{"", "proxy"} {
		if _, err := store.RecordAppSecretRuntimeReload(ctx, state.AppSecretRuntimeReloadResult{
			AccountID: account.ID, AppID: app.ID, InstanceID: instance.ID, WorkloadName: workloadName,
			Fence:    fence,
			Revision: strings.Repeat("a", 64), Projection: state.SecretReloadProjectionUpdated,
			Signal: state.SecretReloadSignalSent, AttemptedAt: time.Now().UTC(),
			Candidates: []state.AppSecretDeliveryCandidate{candidate},
		}); err != nil {
			t.Fatalf("record %q reload: %v", workloadName, err)
		}
		if _, err := store.RecordAppSecretRuntimeReloadAck(ctx, state.AppSecretRuntimeReloadAckResult{
			AccountID: account.ID, AppID: app.ID, InstanceID: instance.ID, WorkloadName: workloadName,
			Fence:    fence,
			Revision: strings.Repeat("a", 64), Status: state.SecretApplicationReloadAckApplied,
			AttemptedAt: time.Now().UTC(), Candidates: []state.AppSecretDeliveryCandidate{candidate},
		}); err != nil {
			t.Fatalf("record %q acknowledgement: %v", workloadName, err)
		}
	}
	targets, err := store.ListAppSecretRuntimeReloadTargets(ctx, account.ID, app.ID, "prod")
	if err != nil || len(targets) != 2 {
		t.Fatalf("reload targets = %+v, err=%v; want separate main and proxy rows", targets, err)
	}
	if targets[0].WorkloadName != "" || targets[1].WorkloadName != "proxy" {
		t.Fatalf("workload target ordering/identity = [%q %q], want main then proxy", targets[0].WorkloadName, targets[1].WorkloadName)
	}
	for _, target := range targets {
		if !target.Reported || target.Version != candidate.Version || target.ApplicationAckVersion != candidate.Version || target.ApplicationAck != state.SecretApplicationReloadAckApplied {
			t.Errorf("%q target did not retain its own report and acknowledgement: %+v", target.WorkloadName, target)
		}
	}
}

func TestMemStoreSecretRevocationAckTracksRemovalAndReintroduction(t *testing.T) {
	store, ctx, account, app := memValueHashFixture(t)
	deployment, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:revocation-ack",
		Status: state.DeployLive, Scope: "prod",
		OverrideEnvSecrets: json.RawMessage(`{"DATABASE_URL":"secret:DATABASE_URL"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentSecretReloadSignal(ctx, deployment.ID, "SIGHUP"); err != nil {
		t.Fatal(err)
	}
	instance, err := store.CreateInstance(ctx, app.ID, deployment.ID, string(state.StateRunning), 256, "node-1", "")
	if err != nil {
		t.Fatal(err)
	}
	upsert := func() {
		t.Helper()
		if err := store.UpsertAppSecretWithKidAndValueHashInScope(ctx, account.ID, app.ID, "prod", "DATABASE_URL", "kid-1", "1111111111111111", []byte("cipher")); err != nil {
			t.Fatal(err)
		}
	}
	upsert()
	first, err := store.DeleteAppSecretInScopeWithRevocation(ctx, account.ID, app.ID, "prod", "DATABASE_URL")
	if err != nil || len(first.Targets) != 1 || first.Targets[0].ReloadSupport != "enabled" {
		t.Fatalf("first revocation = %+v, err=%v; want one enabled target", first, err)
	}

	// A later acknowledgement for a reintroduced key proves the current
	// projection, not that the earlier deletion was observed.
	upsert()
	secrets, err := store.ListAppSecretsInScope(ctx, account.ID, app.ID, "prod")
	if err != nil || len(secrets) != 1 {
		t.Fatalf("reintroduced secret = %+v, err=%v", secrets, err)
	}
	candidate := state.AppSecretDeliveryCandidate{Scope: "prod", Key: "DATABASE_URL", Version: secrets[0].DeliveryVersion}
	ack := state.AppSecretRuntimeReloadAckResult{
		AccountID: account.ID, AppID: app.ID, InstanceID: instance.ID, Revision: strings.Repeat("a", 64),
		Fence:  runtimeSecretFenceForTest(t, store, runtimeAppEnvFixture{account: account, app: app}, deployment),
		Status: state.SecretApplicationReloadAckApplied, AttemptedAt: time.Now().UTC(),
		Candidates: []state.AppSecretDeliveryCandidate{candidate},
	}
	if _, err := store.RecordAppSecretRuntimeReload(ctx, state.AppSecretRuntimeReloadResult{
		AccountID: account.ID, AppID: app.ID, InstanceID: instance.ID, Revision: ack.Revision,
		Fence:      ack.Fence,
		Projection: state.SecretReloadProjectionUpdated, Signal: state.SecretReloadSignalSent,
		AttemptedAt: ack.AttemptedAt, Candidates: ack.Candidates,
	}); err != nil {
		t.Fatalf("record current projection: %v", err)
	}
	if _, err := store.RecordAppSecretRuntimeReloadAck(ctx, ack); err != nil {
		t.Fatalf("acknowledge reintroduced key: %v", err)
	}
	gotFirst, err := store.GetAppSecretRevocation(ctx, account.ID, app.ID, first.ID)
	if err != nil || gotFirst.Targets[0].Status != "pending" {
		t.Fatalf("reintroduced-key acknowledgement changed old revocation: %+v, err=%v", gotFirst, err)
	}

	second, err := store.DeleteAppSecretInScopeWithRevocation(ctx, account.ID, app.ID, "prod", "DATABASE_URL")
	if err != nil {
		t.Fatal(err)
	}
	ack.Candidates = nil // The current, version-fenced projection has no secrets.
	ack.Fence = runtimeSecretFenceForTest(t, store, runtimeAppEnvFixture{account: account, app: app}, deployment)
	ack.AttemptedAt = time.Now().UTC()
	if _, err := store.RecordAppSecretRuntimeReloadAck(ctx, ack); err != nil {
		t.Fatalf("acknowledge empty projection: %v", err)
	}
	for _, revocation := range []state.AppSecretRevocation{first, second} {
		got, err := store.GetAppSecretRevocation(ctx, account.ID, app.ID, revocation.ID)
		status, acknowledged, pending := got.Progress()
		if err != nil || status != "complete" || acknowledged != 1 || pending != 0 {
			// Keep this assertion at the public state boundary: deleted keys
			// cannot be represented as ordinary secret-row candidates.
			t.Fatalf("revocation after empty-projection acknowledgement = %+v, err=%v", got, err)
		}
	}
}
