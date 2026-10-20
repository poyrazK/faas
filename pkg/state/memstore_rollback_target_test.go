package state_test

// Unit tests for MemStore.GetDeploymentByIDScopedToSuperseded
// (SAFE-RELEASES-G, issue #976, PR-G). Mirrors the pgstore-side tests
// in pgstore_rollback_target_test.go so any divergence between the two
// backends is caught at unit-test time (the Store interface contract
// requires both backends to honour the same return-type contract).
//
// Uses the in-process MemStore (no schema, no Postgres) — these run in
// <1ms and don't need the pgtest harness.

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemStore_GetDeploymentByIDScopedToSuperseded(t *testing.T) {
	m := state.NewMemStore()
	ctx := context.Background()

	t.Run("EmptyArgsRejected", func(t *testing.T) {
		if _, err := m.GetDeploymentByIDScopedToSuperseded(ctx, "", "x"); err == nil {
			t.Error("empty appID: expected error, got nil")
		}
		if _, err := m.GetDeploymentByIDScopedToSuperseded(ctx, "x", ""); err == nil {
			t.Error("empty deploymentID: expected error, got nil")
		}
	})

	t.Run("MissingRowReturnsErrNoRollbackTarget", func(t *testing.T) {
		_, app := seedMemAccountAndApp(t, m, ctx)
		_, err := m.GetDeploymentByIDScopedToSuperseded(ctx, app.ID, uuid.NewString())
		if !errors.Is(err, state.ErrNoRollbackTarget) {
			t.Fatalf("missing row: got %v, want ErrNoRollbackTarget", err)
		}
	})

	t.Run("WrongAppReturnsErrNoRollbackTarget", func(t *testing.T) {
		_, app1 := seedMemAccountAndApp(t, m, ctx)
		_, app2 := seedMemAccountAndApp(t, m, ctx)
		d, err := m.CreateDeployment(ctx, state.Deployment{AppID: app1.ID, ImageDigest: "sha256:wrong-app-mem"})
		if err != nil {
			t.Fatalf("CreateDeployment: %v", err)
		}
		if err := m.MarkDeploymentSuperseded(ctx, d.ID); err != nil {
			t.Fatalf("MarkDeploymentSuperseded: %v", err)
		}
		_, err = m.GetDeploymentByIDScopedToSuperseded(ctx, app2.ID, d.ID)
		if !errors.Is(err, state.ErrNoRollbackTarget) {
			t.Fatalf("wrong app: got %v, want ErrNoRollbackTarget", err)
		}
	})

	t.Run("LiveTargetReturnsErrRollbackTargetAlreadyLive", func(t *testing.T) {
		_, app := seedMemAccountAndApp(t, m, ctx)
		d, err := m.CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: "sha256:live-mem"})
		if err != nil {
			t.Fatalf("CreateDeployment: %v", err)
		}
		_, err = m.GetDeploymentByIDScopedToSuperseded(ctx, app.ID, d.ID)
		if !errors.Is(err, state.ErrRollbackTargetAlreadyLive) {
			t.Fatalf("live target: got %v, want ErrRollbackTargetAlreadyLive", err)
		}
	})

	t.Run("SupersededTargetReturns", func(t *testing.T) {
		_, app := seedMemAccountAndApp(t, m, ctx)
		d, err := m.CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: "sha256:superseded-mem"})
		if err != nil {
			t.Fatalf("CreateDeployment: %v", err)
		}
		if err := m.MarkDeploymentSuperseded(ctx, d.ID); err != nil {
			t.Fatalf("MarkDeploymentSuperseded: %v", err)
		}
		got, err := m.GetDeploymentByIDScopedToSuperseded(ctx, app.ID, d.ID)
		if err != nil {
			t.Fatalf("superseded target: unexpected err %v", err)
		}
		if got.ID != d.ID {
			t.Errorf("superseded target: got.ID=%q, want %q", got.ID, d.ID)
		}
		if got.Status != state.DeploySuperseded {
			t.Errorf("superseded target: got.Status=%q, want %q", got.Status, state.DeploySuperseded)
		}
	})

	t.Run("SkipsIntermediateSuperseded", func(t *testing.T) {
		_, app := seedMemAccountAndApp(t, m, ctx)
		d1, _ := m.CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: "sha256:1"})
		d2, _ := m.CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: "sha256:2"})
		d3, _ := m.CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: "sha256:3"})
		for _, id := range []string{d1.ID, d2.ID, d3.ID} {
			if err := m.MarkDeploymentSuperseded(ctx, id); err != nil {
				t.Fatalf("MarkDeploymentSuperseded(%s): %v", id, err)
			}
		}
		got, err := m.GetDeploymentByIDScopedToSuperseded(ctx, app.ID, d2.ID)
		if err != nil {
			t.Fatalf("middle superseded: unexpected err %v", err)
		}
		if got.ID != d2.ID {
			t.Errorf("middle superseded: got.ID=%q, want %q", got.ID, d2.ID)
		}
	})
}

// seedMemAccountAndApp creates an account + app in the in-memory store
// for use by rollback-target tests. Each test invocation gets fresh
// UUIDs so sub-tests don't collide. Mirrors seedPgAccountAndApp at
// pgstore_coverage_sweep_test.go:59 but lives in package state (not
// state_test) so it can drive both backends symmetrically.
func seedMemAccountAndApp(t *testing.T, m *state.MemStore, ctx context.Context) (state.Account, state.App) {
	t.Helper()
	email := "mem-rollback-" + uuid.NewString()[:8] + "@example.com"
	acct, err := m.CreateAccount(ctx, email, api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := m.CreateApp(ctx, state.App{
		ID:        uuid.NewString(),
		AccountID: acct.ID,
		Slug:      "mem-rollback-" + uuid.NewString()[:8],
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	return acct, app
}
