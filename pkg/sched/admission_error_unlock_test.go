// spec: §6.2
// adr: 590
package sched_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type admissionSpecFailureStore struct {
	Store      state.Store
	secretErr  error
	sidecarErr error
}

func (s *admissionSpecFailureStore) RuntimeAppValuesForDeployment(ctx context.Context, accountID, appID, deploymentID string) (state.RuntimeAppValuesSnapshot, error) {
	if s.secretErr != nil {
		return state.RuntimeAppValuesSnapshot{}, s.secretErr
	}
	snapshot, err := s.Store.RuntimeAppValuesForDeployment(ctx, accountID, appID, deploymentID)
	if err == nil && s.sidecarErr != nil {
		snapshot.SidecarLayers = nil
	}
	return snapshot, err
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
				vmm := &sched.FakeVMM{}
				e := sched.XNewEngine(t, statetest.NewStore(store), vmm, &sched.FakeNotifier{}, "1.10.0")
				injected := errors.New("temporary boot-spec read failure")
				if failure == "secrets" {
					store.secretErr = injected
				} else {
					store.sidecarErr = injected
				}
				admit := func() (sched.WakeResult, error) {
					switch path {
					case "wake":
						return e.Wake(ctx, app.ID, "", "", sched.TriggerGateway)
					case "deployment":
						return e.AdmitInstanceForDeployment(ctx, app.ID, dep.ID, "", sched.TriggerFloorDep)
					default:
						return e.AdmitInstance(ctx, app.ID, "", "", sched.TriggerGateway)
					}
				}
				if _, err := admit(); (failure == "secrets" && !errors.Is(err, injected)) ||
					(failure == "sidecars" && (err == nil || !strings.Contains(err.Error(), "has no built layer"))) {
					t.Fatalf("first admission error = %v", err)
				}
				if got := e.Ledger().Concurrency(app.ID); got != 0 {
					t.Errorf("failed admission retained %d reservations", got)
				}
				rows, err := mem.ListInstancesForApp(ctx, app.ID)
				if err != nil {
					t.Fatal(err)
				}
				if failure == "secrets" && len(rows) != 0 {
					t.Fatalf("failed runtime value read created an instance: %+v", rows)
				}
				if failure == "sidecars" && (len(rows) != 1 || rows[0].State != string(state.StateFailed)) {
					t.Fatalf("failed admission rows = %+v", rows)
				}
				if (*vmm.ColdBoots()) != 0 || (*vmm.Restores()) != 0 {
					t.Fatal("invalid boot spec reached vmmd")
				}
				// Check synchronously so the broken code fails immediately instead of
				// leaving an uncancellable retry blocked inside sync.Mutex.Lock.
				mu := e.AppMutex(app.ID)
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
