package sched_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestSidecarEnvReseals: a wake sends vmmd the runtime-domain copy of a
// sidecar env value (ADR 20261022111812) only while the deployment still
// declares the ciphertext the copy was made from.
func TestSidecarEnvReseals(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, app, _ := sched.SeedApp(t, store, api.PlanPro, 256, 2)
	declared := map[string][]byte{"COPIED": []byte("fleet-a"), "STALE": []byte("fleet-b"), "UNCOPIED": []byte("fleet-c")}
	env := map[string]string{}
	for k, v := range declared {
		env[k] = base64.StdEncoding.EncodeToString(v)
	}
	raw, err := json.Marshal(api.Sidecars{{Name: "proxy", Image: "ghcr.io/org/proxy@sha256:01", Type: api.SidecarTypeSidecar, Env: env}})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:sidecar-env", Status: state.DeployLive,
		Scope: api.DefaultEnvScope, Sidecars: raw,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetDeploymentSidecarLayer(ctx, state.DeploymentSidecarLayer{DeploymentID: dep.ID, SidecarName: "proxy", StorageKey: "apps/app/proxy.ext4"}); err != nil {
		t.Fatal(err)
	}
	kid := "age1" + string(bytes.Repeat([]byte("q"), 58))
	for key, source := range map[string][]byte{"COPIED": declared["COPIED"], "STALE": []byte("other bytes")} {
		if ok, err := store.UpsertSidecarEnvReseal(ctx, state.SidecarEnvReseal{
			DeploymentID: dep.ID, SidecarName: "proxy", Key: key, SourceSHA256: state.SidecarEnvSourceDigest(source),
			Kid: kid, Ciphertext: []byte("domain-" + key),
		}, nil); err != nil || !ok {
			t.Fatalf("upsert %s = %v, %v", key, ok, err)
		}
	}

	specs, err := sched.SidecarsForDeploymentOn(ctx, store, dep, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 1 {
		t.Fatalf("specs = %+v, want the proxy", specs)
	}
	got := map[string]string{}
	for _, entry := range specs[0].SealedEnv {
		got[entry.Key] = string(entry.Ciphertext)
	}
	want := map[string]string{"COPIED": "domain-COPIED", "STALE": "fleet-b", "UNCOPIED": "fleet-c"}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("sidecar env %s sent as %q, want %q", key, got[key], value)
		}
	}
}
