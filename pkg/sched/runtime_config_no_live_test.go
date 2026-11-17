// adr: 20261110120000
package sched_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type noLiveRotationStore struct {
	MemStore             *state.MemStore
	objectWakes, pgWakes []string
}

func (s *noLiveRotationStore) FinalizeObjectS3CredentialRotationsForApp(_ context.Context, _ string, wakeID string) error {
	s.objectWakes = append(s.objectWakes, wakeID)
	return nil
}

func (s *noLiveRotationStore) FinalizeManagedPostgresBindingRotationsForApp(_ context.Context, _ string, wakeID string) error {
	s.pgWakes = append(s.pgWakes, wakeID)
	return nil
}

// A credential rotation's runtime refresh for an app without a live
// deployment used to fail on every delivery, leaving the rotation pending.
// With nothing resident it is complete; with instances of a deployment that
// is not live yet it waits for that deployment.
func TestRuntimeRefreshWithoutLiveDeployment(t *testing.T) {
	for _, tc := range []struct {
		name     string
		resident bool
	}{
		{name: "nothing resident finalizes"},
		{name: "resident instances defer", resident: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			mem := state.NewMemStore()
			acct, err := mem.CreateAccount(ctx, "rotation@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := mem.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "rotation", RAMMB: 128, MaxConcurrency: 5, IdleTimeoutS: 60})
			if err != nil {
				t.Fatal(err)
			}
			deployment, err := mem.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc", Status: state.DeploySnapshotting})
			if err != nil {
				t.Fatal(err)
			}
			if tc.resident {
				if _, err := mem.CreateInstance(ctx, app.ID, deployment.ID, "running", 128, state.DefaultLocalNodeName, ""); err != nil {
					t.Fatal(err)
				}
			}
			store := &noLiveRotationStore{MemStore: mem}
			engine := sched.XNewEngine(t, statetest.NewMem(store), &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
			wakeID := uuid.NewString()
			_, err = engine.RefreshRuntimeConfig(ctx, app.ID, wakeID)
			if tc.resident {
				if !errors.Is(err, db.ErrNotificationDeferred) {
					t.Fatalf("refresh = %v, want deferred", err)
				}
				if len(store.objectWakes)+len(store.pgWakes) != 0 {
					t.Fatalf("rotation finalized while instances hold the old values: %v %v", store.objectWakes, store.pgWakes)
				}
				return
			}
			if err != nil {
				t.Fatalf("refresh = %v, want complete", err)
			}
			if len(store.objectWakes) != 1 || store.objectWakes[0] != wakeID || len(store.pgWakes) != 1 || store.pgWakes[0] != wakeID {
				t.Fatalf("finalized object=%v postgres=%v, want %s", store.objectWakes, store.pgWakes, wakeID)
			}
		})
	}
}
