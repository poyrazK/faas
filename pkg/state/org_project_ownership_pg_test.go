//go:build !no_pg

package state_test

// adr: 202610171905

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPostgresOrgProjectOwnership(t *testing.T) {
	orgProjectOwnership(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}

func TestPostgresOrgProjectOwnershipBackfill(t *testing.T) {
	for _, name := range []string{"single tenant", "mixed tenants refused", "shared owner conflict refused"} {
		t.Run(name, func(t *testing.T) {
			mixed := name == "mixed tenants refused"
			conflictingOwner := name == "shared owner conflict refused"
			pool := pgtest.OpenMigrated(t)
			store := state.NewPgStore(pool)
			ctx := t.Context()
			raw, err := migrations.FS.ReadFile("20261019190114312_org_project_ownership_online.sql")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(raw), "-- +goose Down") {
				t.Fatal("migration has no rollback")
			}
			if err := pgtest.ExecMigration(ctx, t, pool, string(raw), false); err != nil {
				t.Fatalf("expand legacy schema: %v", err)
			}
			identity, err := store.CreateAccountWithPersonalOrg(ctx, state.CreateAccountWithPersonalOrgParams{Email: "legacy-project@example.com", Plan: api.PlanPro})
			if err != nil {
				t.Fatal(err)
			}
			org, err := store.CreateOrg(ctx, state.Org{Slug: "legacy-project-company", Name: "Company", Plan: api.PlanFree})
			if err != nil {
				t.Fatal(err)
			}
			legacyOrgID := identity.PersonalOrg.ID
			if conflictingOwner {
				other, err := store.CreateOrg(ctx, state.Org{Slug: "persisted-project-company", Name: "Persisted Owner", Plan: api.PlanFree})
				if err != nil {
					t.Fatal(err)
				}
				legacyOrgID = other.ID
			}
			projectID := uuid.NewString()
			if _, err := pool.Exec(ctx, `INSERT INTO projects(id,account_id,org_id,slug) VALUES($1,$2,$3,'legacy-shop')`, projectID, identity.Account.ID, legacyOrgID); err != nil {
				t.Fatal(err)
			}
			if _, err := store.CreateApp(ctx, state.App{AccountID: identity.Account.ID, OrgID: org.ID, ProjectID: projectID, Slug: "legacy-company-api", WorkloadName: "api"}); err != nil {
				t.Fatal(err)
			}
			if mixed {
				other, err := store.CreateOrg(ctx, state.Org{Slug: "other-legacy-project-company", Name: "Other", Plan: api.PlanFree})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := store.CreateApp(ctx, state.App{AccountID: identity.Account.ID, OrgID: other.ID, ProjectID: projectID, Slug: "mixed-company-api", WorkloadName: "mixed"}); err != nil {
					t.Fatal(err)
				}
			}
			err = pgtest.ExecMigration(ctx, t, pool, string(raw), true)
			if mixed || conflictingOwner {
				if err == nil || !strings.Contains(err.Error(), "remediate mixed project ownership") {
					t.Fatalf("mixed ownership migrated: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ownership backfill: %v", err)
			}
			project, err := store.ProjectByOrgSlug(ctx, org.ID, "legacy-shop")
			if err != nil || project.ID != projectID || project.AccountID != identity.Account.ID {
				t.Fatalf("backfill rewrote resource identity: %+v %v", project, err)
			}
			if _, err := pool.Exec(ctx, `UPDATE projects SET org_id=$2 WHERE id=$1`, projectID, identity.PersonalOrg.ID); err == nil {
				t.Fatal("raw project ownership rewrite accepted")
			}
			if _, err := pool.Exec(ctx, `INSERT INTO apps(account_id,org_id,project_id,slug,type,ram_mb,max_concurrency) VALUES($1,$2,$3,'wrong-owner','app',128,1)`, identity.Account.ID, identity.PersonalOrg.ID, projectID); err == nil {
				t.Fatal("cross-organization project workload accepted")
			}
		})
	}
}

func TestPostgresOrgProjectOffboardingCommitOrder(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	f := setupOffboarding(t, store)
	ctx := t.Context()
	project, err := store.CreateProject(ctx, state.Project{AccountID: f.actor.ID, OrgID: f.org.ID, Slug: "ordered-project"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.OrgAccessSnapshot(ctx, f.org.ID, f.actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	fenced, cancel := state.WithOrgAccessFence(ctx, state.OrgAccessFence{OrgID: f.org.ID, AccountID: f.actor.ID, Revision: snapshot.Revision})
	defer cancel(nil)
	lock, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Rollback(ctx) }()
	var blockerPID int32
	if err := lock.QueryRow(ctx, `SELECT pg_backend_pid() FROM projects WHERE id=$1 FOR UPDATE`, project.ID).Scan(&blockerPID); err != nil {
		t.Fatal(err)
	}
	values, hash, err := api.NormalizeProjectEnvironmentConfig(json.RawMessage(`{"ORDER":"before-removal"}`))
	if err != nil {
		t.Fatal(err)
	}
	writeDone := make(chan error, 1)
	go func() {
		_, err := store.CreateProjectEnvironmentConfigVersion(fenced, state.ProjectEnvironmentConfig{AccountID: project.AccountID, ProjectID: project.ID, EnvironmentSlug: "production", ConfigHash: hash, Values: values})
		writeDone <- err
	}()
	waitForOffboardingBlock(t, pool, blockerPID)
	var writerPID int32
	if err := pool.QueryRow(ctx, `SELECT pid FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)) ORDER BY pid LIMIT 1`, blockerPID).Scan(&writerPID); err != nil {
		t.Fatal(err)
	}
	removeDone := make(chan error, 1)
	go func() { removeDone <- store.RemoveOrgMember(ctx, f.org.ID, f.actor.ID) }()
	waitForOffboardingBlock(t, pool, writerPID)
	if err := lock.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	for _, done := range []chan error{writeDone, removeDone} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("project write/removal did not finish")
		}
	}
	saved, err := store.ProjectEnvironmentConfigLatest(ctx, project.AccountID, project.ID, "production")
	if err != nil || saved.Version != 1 {
		t.Fatalf("authorized write lost commit ordering: %+v %v", saved, err)
	}
	fresh, stop := state.WithOrgAccessFence(ctx, state.OrgAccessFence{OrgID: f.org.ID, AccountID: f.actor.ID, Revision: snapshot.Revision})
	defer stop(nil)
	if _, err := store.CreateProjectEnvironment(fresh, state.ProjectEnvironment{AccountID: project.AccountID, ProjectID: project.ID, Slug: "after-removal"}); !errors.Is(err, state.ErrOrgAccessRevoked) {
		t.Fatalf("stale project mutation survived removal: %v", err)
	}
}
