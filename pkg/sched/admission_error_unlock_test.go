package sched

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type admissionSpecFailureStore struct {
	state.Store
	secretErr  error
	sidecarErr error
}

func (s *admissionSpecFailureStore) ListAppSecretsInScope(ctx context.Context, accountID, appID, scope string) ([]state.AppSecret, error) {
	if s.secretErr != nil {
		return nil, s.secretErr
	}
	return s.Store.ListAppSecretsInScope(ctx, accountID, appID, scope)
}

func (s *admissionSpecFailureStore) ListDeploymentSidecarLayers(ctx context.Context, deploymentID string) ([]state.DeploymentSidecarLayer, error) {
	if s.sidecarErr != nil {
		return nil, s.sidecarErr
	}
	return s.Store.ListDeploymentSidecarLayers(ctx, deploymentID)
}

func TestAdmissionSpecFailureReleasesAppLockAndAllowsRetry(t *testing.T) {
	for _, path := range []string{"wake", "admit", "deployment"} {
		for _, failure := range []string{"secrets", "sidecars"} {
			t.Run(path+"/"+failure, func(t *testing.T) {
				ctx := context.Background()
				mem := state.NewMemStore()
				acct, err := mem.CreateAccount(ctx, "unlock@example.com", api.PlanPro)
				if err != nil {
					t.Fatal(err)
				}
				app, err := mem.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "unlock", RAMMB: 128, MaxConcurrency: 5, IdleTimeoutS: 60})
				if err != nil {
					t.Fatal(err)
				}
				dep, err := mem.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc", Status: state.DeployLive, Sidecars: json.RawMessage(`[{"name":"metrics","type":"sidecar"}]`)})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := mem.SetDeploymentSidecarLayer(ctx, state.DeploymentSidecarLayer{DeploymentID: dep.ID, SidecarName: "metrics", StorageKey: "apps/a/metrics.ext4"}); err != nil {
					t.Fatal(err)
				}
				store := &admissionSpecFailureStore{Store: mem}
				vmm := &fakeVMM{}
				e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
				injected := errors.New("temporary boot-spec read failure")
				if failure == "secrets" {
					store.secretErr = injected
				} else {
					store.sidecarErr = injected
				}
				admit := func() (WakeResult, error) {
					switch path {
					case "wake":
						return e.Wake(ctx, app.ID, "", "", TriggerGateway)
					case "deployment":
						return e.AdmitInstanceForDeployment(ctx, app.ID, dep.ID, "", TriggerFloorDep)
					default:
						return e.AdmitInstance(ctx, app.ID, "", "", TriggerGateway)
					}
				}
				if _, err := admit(); !errors.Is(err, injected) {
					t.Fatalf("first admission error = %v", err)
				}
				if got := e.Ledger().Concurrency(app.ID); got != 0 {
					t.Errorf("failed admission retained %d reservations", got)
				}
				rows, err := mem.ListInstancesForApp(ctx, app.ID)
				if err != nil {
					t.Fatal(err)
				}
				if len(rows) != 1 || rows[0].State != string(state.StateFailed) {
					t.Fatalf("failed admission rows = %+v", rows)
				}
				if vmm.coldBoots != 0 || vmm.restores != 0 {
					t.Fatal("invalid boot spec reached vmmd")
				}
				// Check synchronously so the broken code fails immediately instead of
				// leaving an uncancellable retry blocked inside sync.Mutex.Lock.
				mu := e.appMutex(app.ID)
				if !mu.TryLock() {
					t.Fatal("failed admission left the app mutex locked; subsequent wakes would hang")
				}
				mu.Unlock()
				store.secretErr, store.sidecarErr = nil, nil
				res, err := admit()
				if err != nil {
					t.Fatalf("retry after transient failure: %v", err)
				}
				ins, err := mem.InstanceByID(ctx, res.InstanceID)
				if err != nil || ins.State != string(state.StateRunning) {
					t.Fatalf("retry instance = %+v, err %v", ins, err)
				}
				if got := e.Ledger().Concurrency(app.ID); got != 1 {
					t.Fatalf("retry reservations = %d, want 1", got)
				}
			})
		}
	}
}
