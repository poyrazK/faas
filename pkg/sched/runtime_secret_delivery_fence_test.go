// adr: 531
package sched

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type runtimeSecretDeliveryTrackingStore struct {
	state.Store
	result state.AppSecretDeliveryResult
	err    error
	calls  int
}

func (s *runtimeSecretDeliveryTrackingStore) RecordAppSecretDelivery(ctx context.Context, result state.AppSecretDeliveryResult) (int, error) {
	s.calls++
	s.result = result
	count, err := s.Store.RecordAppSecretDelivery(ctx, result)
	s.err = err
	return count, err
}

func TestEngineBootSecretDeliveryUsesSelectedFence(t *testing.T) {
	for _, prime := range []bool{false, true} {
		name := "wake"
		if prime {
			name = "prime"
		}
		for _, change := range []string{"none", "rotation", "same-envelope-recreation", "failed-start"} {
			t.Run(name+"/"+change, func(t *testing.T) {
				ctx := t.Context()
				mem := state.NewMemStore()
				account, app, _ := seedApp(t, mem, api.PlanPro, 256, 5)
				status := state.DeployLive
				if prime {
					status = state.DeploySnapshotting
					manifest := app.Manifest
					manifest.ExecutionMode = api.ExecutionModeWorker
					var err error
					app, err = mem.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &manifest})
					if err != nil {
						t.Fatal(err)
					}
				}
				dep, err := mem.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage,
					Status: status, ImageDigest: "sha256:owned-boot",
					OverrideEnvSecrets: json.RawMessage(`{"MAIN_TOKEN":"secret:MAIN_TOKEN"}`),
					Sidecars:           json.RawMessage(`[{"name":"proxy","type":"sidecar","env_secrets":{"SIDECAR_TOKEN":"secret:SIDECAR_TOKEN"}}]`)})
				if err != nil {
					t.Fatal(err)
				}
				if !prime {
					if err := mem.MarkDeploymentLive(ctx, dep.ID); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := mem.SetDeploymentSidecarLayer(ctx, state.DeploymentSidecarLayer{DeploymentID: dep.ID, SidecarName: "proxy", StorageKey: "apps/owned-boot/proxy.ext4"}); err != nil {
					t.Fatal(err)
				}
				for _, key := range []string{"MAIN_TOKEN", "SIDECAR_TOKEN", "UNGRANTED_TOKEN"} {
					if err := mem.UpsertAppSecret(ctx, account.ID, app.ID, key, []byte(key+"-sealed")); err != nil {
						t.Fatal(err)
					}
				}
				before, err := mem.RuntimeAppValuesForDeployment(ctx, account.ID, app.ID, dep.ID)
				if err != nil {
					t.Fatal(err)
				}
				selectedFence, err := state.NewRuntimeAppSecretFence(before)
				if err != nil {
					t.Fatal(err)
				}
				store := &runtimeSecretDeliveryTrackingStore{Store: mem}
				vmm := &fakeVMM{}
				if change == "failed-start" {
					vmm.wakeErr = errors.New("test boot failure")
				}
				vmm.coldBootHook = func() {
					if change == "same-envelope-recreation" {
						if err := mem.DeleteAppSecretInScope(ctx, account.ID, app.ID, api.DefaultEnvScope, "SIDECAR_TOKEN"); err != nil {
							t.Fatal(err)
						}
					}
					if change == "rotation" || change == "same-envelope-recreation" {
						cipher := []byte("SIDECAR_TOKEN-sealed")
						if change == "rotation" {
							cipher = []byte("rotated-sidecar-sealed")
						}
						if err := mem.UpsertAppSecret(ctx, account.ID, app.ID, "SIDECAR_TOKEN", cipher); err != nil {
							t.Fatal(err)
						}
					}
				}
				engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
				if prime {
					err = engine.Prime(ctx, app.ID, dep.ID)
				} else {
					_, err = engine.Wake(ctx, app.ID, "", "", "")
				}
				stale := change == "rotation" || change == "same-envelope-recreation"
				if (change == "failed-start" || stale) != (err != nil) {
					t.Fatalf("boot result = %v for %s", err, change)
				}
				if store.calls != 1 || store.result.Fence != selectedFence || len(store.result.Candidates) != 2 {
					t.Fatalf("boot completion lost selected main/sidecar ownership: calls=%d result=%+v", store.calls, store.result)
				}
				if stale != errors.Is(store.err, state.ErrConflict) || (!stale && store.err != nil) {
					t.Fatalf("completion fence error = %v for %s", store.err, change)
				}
				instance, err := mem.InstanceByID(ctx, store.result.InstanceID)
				if err != nil || instance.WakeID != store.result.WakeID || instance.DeploymentID != dep.ID {
					t.Fatalf("boot completion detached from wake attempt: %+v %v", instance, err)
				}
				if stale && (instance.State != string(state.StateFailed) || vmm.destroys != 1 || engine.ledger.ResidentRAM() != 0) {
					t.Fatalf("changed sealed inputs retained a published runtime: %+v destroys=%d ram=%d", instance, vmm.destroys, engine.ledger.ResidentRAM())
				}
				for _, key := range []string{"MAIN_TOKEN", "SIDECAR_TOKEN", "UNGRANTED_TOKEN"} {
					row, err := mem.GetAppSecret(ctx, account.ID, app.ID, key)
					if err != nil {
						t.Fatal(err)
					}
					want := state.SecretDeliveryDelivered
					if stale || key == "UNGRANTED_TOKEN" {
						want = state.SecretDeliveryPending
					} else if change == "failed-start" {
						want = state.SecretDeliveryFailed
					}
					if row.DeliveryStatus != want {
						t.Errorf("%s delivery = %s want %s", key, row.DeliveryStatus, want)
					}
				}
			})
		}
	}
}
