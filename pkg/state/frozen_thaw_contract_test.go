// adr: 1740 — a ticketed thaw publishes only while every guard the ordinary
// wake path would have read still holds.
package state_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemFrozenThawGuards(t *testing.T) {
	testFrozenThawGuards(t, state.NewMemStore())
}

func TestMemFrozenThawSelectionAgreesWithWake(t *testing.T) {
	testFrozenThawSelectionAgreesWithWake(t, state.NewMemStore())
}

// seedFrozenThaw freezes a published instance of a live production
// deployment and returns the thaw its ticket would request.
func seedFrozenThaw(t *testing.T, store runtimeAppEnvTestStore, node string) (runtimeAppEnvFixture, state.FrozenThaw) {
	t.Helper()
	ctx := t.Context()
	f := seedRuntimeAppEnv(t, store)
	dep := f.deployments["production"]
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	p := seedRuntimeInstancePublication(t, store, f, dep, node)
	if _, err := store.PublishOwnedInstanceRuntime(ctx, p); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if err := store.UpdateInstanceState(ctx, p.InstanceID, string(state.StateFrozen)); err != nil {
		t.Fatalf("freeze: %v", err)
	}
	basis, err := store.(state.FrozenThawStore).FrozenThawBasis(ctx, f.app.ID, dep.ID)
	if err != nil || !basis.Selected || basis.Generation <= 0 {
		t.Fatalf("basis = %+v, %v; want the deployment selected", basis, err)
	}
	return f, state.FrozenThaw{AccountID: f.account.ID, AppID: f.app.ID, InstanceID: p.InstanceID, NodeID: p.NodeID, WakeID: p.WakeID,
		Fence: p.ConfigFence, Generation: basis.Generation,
		Bookkeeping: &state.RuntimePublicationBookkeeping{EventKind: "frozen_thaw", EventData: []byte(`{"from":"frozen","to":"running"}`)}}
}

func testFrozenThawGuards(t *testing.T, store runtimeAppEnvTestStore) {
	thawer := store.(state.FrozenThawStore)
	node := runtimeSecretNodeForTest(t, store)
	ram, scale := 512, api.PlanScale
	for _, tc := range []struct {
		name   string
		change func(context.Context, runtimeAppEnvFixture, *state.FrozenThaw) error
	}{
		{name: "another wake", change: func(_ context.Context, _ runtimeAppEnvFixture, thaw *state.FrozenThaw) error {
			thaw.WakeID = uuid.NewString()
			return nil
		}},
		{name: "another config fingerprint", change: func(_ context.Context, _ runtimeAppEnvFixture, thaw *state.FrozenThaw) error {
			thaw.Fence.Fingerprint = flipLastHex(thaw.Fence.Fingerprint)
			return nil
		}},
		{name: "app resized", change: func(ctx context.Context, f runtimeAppEnvFixture, _ *state.FrozenThaw) error {
			_, err := store.UpdateApp(ctx, f.app.ID, state.UpdateAppParams{RAMMB: &ram})
			return err
		}},
		{name: "app deleted", change: func(ctx context.Context, f runtimeAppEnvFixture, _ *state.FrozenThaw) error {
			return store.DeleteApp(ctx, f.app.ID)
		}},
		{name: "account suspended", change: func(ctx context.Context, f runtimeAppEnvFixture, _ *state.FrozenThaw) error {
			return store.UpdateAccountStatus(ctx, f.account.ID, state.AccountSuspended)
		}},
		{name: "plan changed", change: func(ctx context.Context, f runtimeAppEnvFixture, _ *state.FrozenThaw) error {
			return store.UpdateAccountPlan(ctx, f.account.ID, scale)
		}},
		{name: "newer deployment live", change: func(ctx context.Context, f runtimeAppEnvFixture, _ *state.FrozenThaw) error {
			newer, err := store.CreateDeployment(ctx, state.Deployment{AppID: f.app.ID, Scope: "production", Kind: state.DeploymentKindImage})
			if err != nil {
				return err
			}
			return store.MarkDeploymentLive(ctx, newer.ID)
		}},
		{name: "runtime config stamped", change: func(ctx context.Context, f runtimeAppEnvFixture, _ *state.FrozenThaw) error {
			return store.MarkAppRuntimeConfigChanged(ctx, f.app.ID)
		}},
		// A value written without a runtime-config stamp still moves the
		// generation (ADR-1741), in the write's own transaction.
		{name: "environment variable edited", change: func(ctx context.Context, f runtimeAppEnvFixture, _ *state.FrozenThaw) error {
			return store.UpsertAppEnvInScope(ctx, f.account.ID, f.app.ID, "production", "MODE", "edited")
		}},
		{name: "secret added", change: func(ctx context.Context, f runtimeAppEnvFixture, _ *state.FrozenThaw) error {
			return store.UpsertAppSecretInScope(ctx, f.account.ID, f.app.ID, "production", "TOKEN", []byte("added"))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			f, thaw := seedFrozenThaw(t, store, node)
			if err := tc.change(ctx, f, &thaw); err != nil {
				t.Fatalf("change: %v", err)
			}
			if _, err := thawer.ThawFrozenInstance(ctx, thaw); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("thaw after %s = %v, want ErrConflict", tc.name, err)
			}
			row, err := store.InstanceByID(ctx, thaw.InstanceID)
			if err != nil || row.State != string(state.StateFrozen) {
				t.Fatalf("instance after a refused thaw = %q, %v; want frozen", row.State, err)
			}
			if events, err := store.ListEvents(ctx, thaw.InstanceID, 10); err != nil || len(events) != 0 {
				t.Fatalf("a refused thaw recorded events: %+v, %v", events, err)
			}
		})
	}

	t.Run("guards hold", func(t *testing.T) {
		ctx := t.Context()
		_, thaw := seedFrozenThaw(t, store, node)
		ins, err := thawer.ThawFrozenInstance(ctx, thaw)
		if err != nil || ins.State != string(state.StateRunning) || ins.ID != thaw.InstanceID || ins.WakeID != thaw.WakeID {
			t.Fatalf("thaw = %+v, %v; want the instance running", ins, err)
		}
		if events, err := store.ListEvents(ctx, thaw.InstanceID, 10); err != nil || len(events) != 1 || events[0].Kind != "frozen_thaw" {
			t.Fatalf("events = %+v, %v; want one frozen_thaw", events, err)
		}
		if _, err := thawer.ThawFrozenInstance(ctx, thaw); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("second thaw = %v, want ErrConflict", err)
		}
	})
}

// The guard's selection rule must never choose a deployment the ordinary
// wake path would not: whenever the basis says selected, production
// resolution returns the same deployment.
func testFrozenThawSelectionAgreesWithWake(t *testing.T, store runtimeAppEnvTestStore) {
	node := runtimeSecretNodeForTest(t, store)
	for _, tc := range []struct {
		name         string
		change       func(context.Context, runtimeAppEnvFixture) error
		wantSelected bool
	}{
		{name: "only live deployment", wantSelected: true},
		{name: "release pins it", wantSelected: true, change: func(ctx context.Context, f runtimeAppEnvFixture) error {
			// A release may pin an app only for as long as its manifest allows.
			app, err := store.AppByID(ctx, f.app.ID)
			if err != nil {
				return err
			}
			manifest := app.Manifest
			manifest.RevisionPinTTLSeconds = 3600
			if _, err := store.UpdateApp(ctx, f.app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
				return err
			}
			_, err = store.(state.ProjectReleaseSetStore).PublishProjectReleaseSet(ctx, f.account.ID, f.project.ID, "production", 1800,
				[]state.ProjectReleaseMember{{AppID: f.app.ID, DeploymentID: f.deployments["production"].ID}})
			return err
		}},
		{name: "newer deployment live", change: func(ctx context.Context, f runtimeAppEnvFixture) error {
			newer, err := store.CreateDeployment(ctx, state.Deployment{AppID: f.app.ID, Scope: "production", Kind: state.DeploymentKindImage})
			if err != nil {
				return err
			}
			return store.MarkDeploymentLive(ctx, newer.ID)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			f, _ := seedFrozenThaw(t, store, node)
			if tc.change != nil {
				if err := tc.change(ctx, f); err != nil {
					t.Fatalf("change: %v", err)
				}
			}
			dep := f.deployments["production"]
			basis, err := store.(state.FrozenThawStore).FrozenThawBasis(ctx, f.app.ID, dep.ID)
			if err != nil || basis.Selected != tc.wantSelected {
				t.Fatalf("basis selected = %v, %v; want %v", basis.Selected, err, tc.wantSelected)
			}
			resolved, err := state.ResolveProductionDeployment(ctx, store, f.app.ID)
			if basis.Selected && (err != nil || resolved.ID != dep.ID) {
				t.Fatalf("guard selects %s but the wake path resolves %s, %v", dep.ID, resolved.ID, err)
			}
		})
	}
}

// flipLastHex returns a fingerprint of the same shape that differs.
func flipLastHex(fingerprint string) string {
	last := fingerprint[len(fingerprint)-1]
	next := byte('0')
	if last == '0' {
		next = '1'
	}
	return fingerprint[:len(fingerprint)-1] + string(next)
}
