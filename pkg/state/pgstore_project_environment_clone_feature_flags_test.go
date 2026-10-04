//go:build !no_pg

// adr: 581
package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgProjectEnvironmentCloneFeatureFlagsIsolation(t *testing.T) {
	s, _, _ := pgWithPool(t)
	projectEnvironmentCloneFeatureFlagsIsolation(t, s)
}

func TestPgProjectEnvironmentCloneFeatureFlagsPublication(t *testing.T) {
	for _, fault := range []string{"before_flags", "after_flags", "after_flags_same_config"} {
		t.Run(fault, func(t *testing.T) {
			s, _, _ := pgWithPool(t)
			projectEnvironmentClonePublicationContract(t, s, false, fault)
		})
	}
}

// A flag writer waiting behind a clone's project lock must not hold the source
// environment lock. Otherwise the clone and writer can deadlock on FK checks.
func TestPgProjectEnvironmentCloneFeatureFlagLockOrder(t *testing.T) {
	s, ctx, pool := pgWithPool(t)
	a, err := s.CreateAccount(ctx, "flag-lock-order@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.CreateProject(ctx, state.Project{AccountID: a.ID, Slug: "flag-lock-order"})
	if err != nil {
		t.Fatal(err)
	}
	env, err := s.ProjectEnvironmentBySlug(ctx, a.ID, p.ID, "production")
	if err != nil {
		t.Fatal(err)
	}
	holder, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Rollback(context.Background()) }()
	if _, err := holder.Exec(ctx, "select id from projects where id=$1 for update", p.ID); err != nil {
		t.Fatal(err)
	}
	writerCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, err := s.UpdateFeatureFlags(writerCtx, state.FeatureFlagUpdate{Scope: state.FeatureFlagScope{AccountID: a.ID, ProjectID: p.ID, EnvironmentID: env.ID},
			Config: flags.Config{Flags: []flags.Flag{{Key: "lock_order", Enabled: true, Default: true}}}, Actor: "developer"})
		finished <- err
	}()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		if err := pool.QueryRow(writerCtx, "select exists(select 1 from pg_stat_activity where pid<>pg_backend_pid() and datname=current_database() and wait_event_type='Lock' and query like '%LockFeatureFlagProject%')").Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err := <-finished:
			t.Fatalf("flag writer did not wait behind the clone: %v", err)
		case <-writerCtx.Done():
			t.Fatal("flag writer never reached the project lock")
		case <-ticker.C:
		}
	}
	if _, err := holder.Exec(ctx, "select id from project_environments where id=$1 for update nowait", env.ID); err != nil {
		t.Fatalf("flag writer took environment before project: %v", err)
	}
	if err := holder.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-finished; err != nil {
		t.Fatalf("flag writer did not recover after clone lock: %v", err)
	}
}
