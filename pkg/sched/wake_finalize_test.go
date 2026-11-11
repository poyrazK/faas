// adr: 20261105090246
package sched_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

// heldSecretDeliveryStore holds every secret delivery record until release
// closes, so a test can tell whether a wake answered before its bookkeeping.
type heldSecretDeliveryStore struct {
	Store   state.Store
	release chan struct{}
	mu      sync.Mutex
	calls   int
}

func (s *heldSecretDeliveryStore) RecordAppSecretDelivery(ctx context.Context, result state.AppSecretDeliveryResult) (int, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	select {
	case <-s.release:
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	return s.Store.RecordAppSecretDelivery(ctx, result)
}

func (s *heldSecretDeliveryStore) recordCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func TestBootedWakeFinalize(t *testing.T) {
	for _, tc := range []struct {
		name  string
		async bool
		// beforeAnswer is the secret's delivery status when Wake returns.
		beforeAnswer state.SecretDeliveryStatus
		// beforeClass is the app's workload class when Wake returns, or ""
		// when the background bookkeeping may or may not have recorded it.
		beforeClass state.WorkloadClass
	}{
		{name: "inline, before the answer", beforeAnswer: state.SecretDeliveryDelivered, beforeClass: state.WorkloadClassGraphQL},
		{name: "async_wake_finalize, after the answer", async: true, beforeAnswer: state.SecretDeliveryPending},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			mem := state.NewMemStore()
			account, app, _ := sched.SeedApp(t, mem, api.PlanPro, 512, 5)
			if _, err := mem.SetAppWorkloadClass(ctx, app.ID, state.WorkloadClassHTTP, "scan_hint"); err != nil {
				t.Fatal(err)
			}
			dep, err := mem.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage,
				Status: state.DeployLive, ImageDigest: "sha256:finalize",
				OverrideEnvSecrets: json.RawMessage(`{"MAIN_TOKEN":"secret:MAIN_TOKEN"}`)})
			if err != nil {
				t.Fatal(err)
			}
			if err := mem.MarkDeploymentLive(ctx, dep.ID); err != nil {
				t.Fatal(err)
			}
			if err := mem.UpsertAppSecret(ctx, account.ID, app.ID, "MAIN_TOKEN", []byte("MAIN_TOKEN-sealed")); err != nil {
				t.Fatal(err)
			}
			store := &heldSecretDeliveryStore{Store: mem, release: make(chan struct{})}
			vmm := &sched.FakeVMM{}
			*vmm.Characterization() = api.CharacterizationReport{ObservedClass: string(state.WorkloadClassGraphQL), ObservedPort: 4000}
			e := sched.XNewEngine(t, statetest.NewStore(store), vmm, &sched.FakeNotifier{}, "1.10.0")
			if tc.async {
				e.WithAsyncWakeFinalize()
			} else {
				close(store.release)
			}

			answered := make(chan error, 1)
			go func() {
				_, err := e.Wake(ctx, app.ID, "", "", "")
				answered <- err
			}()
			select {
			case err := <-answered:
				if err != nil {
					t.Fatalf("Wake: %v", err)
				}
			case <-time.After(10 * time.Second):
				close(store.release)
				t.Fatal("Wake waited for its held secret delivery record")
			}
			assertSecretDelivery(t, mem, account.ID, app.ID, tc.beforeAnswer)
			if tc.beforeClass != "" {
				assertWorkloadClass(t, mem, app.ID, tc.beforeClass)
			}

			if tc.async {
				waitCtx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
				err := e.WaitWakeFinalize(waitCtx)
				cancel()
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("WaitWakeFinalize with the record held = %v, want deadline exceeded", err)
				}
				close(store.release)
			}
			if err := e.WaitWakeFinalize(ctx); err != nil {
				t.Fatalf("WaitWakeFinalize: %v", err)
			}
			if calls := store.recordCalls(); calls != 1 {
				t.Fatalf("secret delivery records = %d, want 1", calls)
			}
			assertSecretDelivery(t, mem, account.ID, app.ID, state.SecretDeliveryDelivered)
			assertWorkloadClass(t, mem, app.ID, state.WorkloadClassGraphQL)
		})
	}
}

func assertSecretDelivery(t *testing.T, mem *state.MemStore, accountID, appID string, want state.SecretDeliveryStatus) {
	t.Helper()
	row, err := mem.GetAppSecret(t.Context(), accountID, appID, "MAIN_TOKEN")
	if err != nil {
		t.Fatal(err)
	}
	if row.DeliveryStatus != want {
		t.Fatalf("MAIN_TOKEN delivery = %s, want %s", row.DeliveryStatus, want)
	}
}

func assertWorkloadClass(t *testing.T, mem *state.MemStore, appID string, want state.WorkloadClass) {
	t.Helper()
	got, err := mem.AppByID(t.Context(), appID)
	if err != nil {
		t.Fatal(err)
	}
	if got.WorkloadClass != want {
		t.Fatalf("workload class = %q, want %q", got.WorkloadClass, want)
	}
}
