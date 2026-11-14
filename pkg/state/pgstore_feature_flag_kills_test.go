//go:build !no_pg

package state_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR 20261114150000 §3: gatewayd reads a deployment's killed flags from its
// project environment's latest configuration, and every published version
// fires feature_flags_changed.
func TestPg_FeatureFlagKillsForDeployment(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	acct, appID, depID := seedLiveDeploy(t, s, ctx)

	if keys, err := s.FeatureFlagKillsForDeployment(ctx, depID); err != nil || len(keys) != 0 {
		t.Fatalf("app outside a project: %v %v", keys, err)
	}

	project, err := s.CreateProject(ctx, state.Project{AccountID: acct, Slug: "flags-kill", ProductionBranch: "main", ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE apps SET project_id = $1 WHERE id = $2`, project.ID, appID); err != nil {
		t.Fatalf("attach app to project: %v", err)
	}
	env, err := s.ProjectEnvironmentBySlug(ctx, acct, project.ID, "production")
	if err != nil {
		t.Fatalf("production environment: %v", err)
	}
	scope := state.FeatureFlagScope{AccountID: acct, ProjectID: project.ID, EnvironmentID: env.ID}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "LISTEN "+db.NotifyFeatureFlagsChanged); err != nil {
		t.Fatal(err)
	}

	publish := func(expected int64, cfg flags.Config) {
		t.Helper()
		if _, err := s.UpdateFeatureFlags(ctx, state.FeatureFlagUpdate{Scope: scope, ExpectedVersion: expected, Config: cfg, Actor: acct}); err != nil {
			t.Fatalf("UpdateFeatureFlags: %v", err)
		}
		waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		n, err := conn.Conn().WaitForNotification(waitCtx)
		if err != nil || n.Channel != db.NotifyFeatureFlagsChanged || n.Payload != env.ID {
			t.Fatalf("notification = %+v, %v; want %s on %s", n, err, env.ID, db.NotifyFeatureFlagsChanged)
		}
	}

	publish(0, flags.Config{Flags: []flags.Flag{
		{Key: "zeta", Enabled: true, Killed: true},
		{Key: "alive", Enabled: true},
		{Key: "alpha", Enabled: true, Killed: true},
	}})
	keys, err := s.FeatureFlagKillsForDeployment(ctx, depID)
	if err != nil || !slices.Equal(keys, []string{"alpha", "zeta"}) {
		t.Fatalf("kills = %v, %v", keys, err)
	}

	publish(1, flags.Config{Flags: []flags.Flag{{Key: "zeta", Enabled: true}, {Key: "alpha", Enabled: true}}})
	if keys, err := s.FeatureFlagKillsForDeployment(ctx, depID); err != nil || len(keys) != 0 {
		t.Fatalf("after unkill: %v %v", keys, err)
	}
	if keys, err := s.FeatureFlagKillsForDeployment(ctx, "not-a-uuid"); err != nil || keys != nil {
		t.Fatalf("invalid id: %v %v", keys, err)
	}
}
