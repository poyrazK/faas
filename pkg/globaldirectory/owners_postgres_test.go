//go:build !no_pg

package globaldirectory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func orgDirectoryFixture(t *testing.T) (*pgxpool.Pool, *PgStore, *state.PgStore, Owner, state.Org, state.Account) {
	t.Helper()
	pool := directoryPG(t)
	authority := state.NewPgStore(pool)
	actor, err := authority.CreateAccount(t.Context(), uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	org, err := authority.CreateOrg(t.Context(), state.Org{Slug: "global-team", Name: "Global Team", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err = authority.AddOrgMember(t.Context(), org.ID, actor.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	store := NewPgStore(pool)
	owner, err := store.ResolveOrgOwner(t.Context(), org.ID)
	if err != nil {
		t.Fatal(err)
	}
	return pool, store, authority, owner, org, actor
}
func TestGlobalOrganizationPostgresQuotaAndIdentityRace(t *testing.T) {
	pool, s, _, owner, org, actor := orgDirectoryFixture(t)
	var successes atomic.Int32
	var wg sync.WaitGroup
	for i := range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			app, err := s.CreateOrgApp(t.Context(), owner.ID, actor.ID, org.Plan, fmt.Sprintf("org-app-%02d", i), 3)
			if err == nil {
				if app.OwnerID != owner.ID {
					t.Error("actor became owner")
				}
				successes.Add(1)
			} else if !errors.Is(err, ErrLimit) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 3 {
		t.Fatalf("quota winners: %d", successes.Load())
	}
	o, err := s.ResolveOrgOwner(t.Context(), org.ID)
	if err != nil || o != owner {
		t.Fatalf("owner retry: %+v %v", o, err)
	}
	if _, err = pool.Exec(t.Context(), "UPDATE global_directory_owners SET org_id=$1 WHERE id=$2", uuid.NewString(), owner.ID); err == nil {
		t.Fatal("owner reassigned")
	}
	for _, q := range []string{"DELETE FROM global_directory_owners WHERE id=$1", "TRUNCATE global_directory_owners CASCADE"} {
		args := []any{}
		if strings.Contains(q, "$1") {
			args = append(args, owner.ID)
		}
		if _, err = pool.Exec(t.Context(), q, args...); err == nil {
			t.Fatalf("root deletion bypass: %s", q)
		}
	}
	if _, err = s.CreateApp(t.Context(), owner.ID, "personal-bypass", 3); !errors.Is(err, ErrConflict) {
		t.Fatalf("personal bypass: %v", err)
	}
	cell, err := s.PutCell(t.Context(), Cell{ID: uuid.NewString(), Region: "eu-fsn", APIBase: "https://fsn.example.com", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	b := Binding{CellID: cell.ID, Region: cell.Region, CellGeneration: cell.Generation, RegionalAccountID: uuid.NewString(), RegionalAppID: uuid.NewString(), RegionalAppSlug: "regional-app"}
	apps, err := s.ListApps(t.Context(), owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AttachTarget(t.Context(), owner.ID, apps[0].Slug, b); !errors.Is(err, ErrOrgTargetsUnavailable) {
		t.Fatalf("shared target bypass: %v", err)
	}
	if _, err = pool.Exec(t.Context(), "INSERT INTO global_app_targets(global_app_id,owner_id,cell_id,region,cell_generation,regional_account_id,regional_app_id,regional_app_slug) VALUES($1,$2,$3,$4,1,$5,$6,'regional-app')", apps[0].ID, owner.ID, cell.ID, cell.Region, b.RegionalAccountID, b.RegionalAppID); err == nil {
		t.Fatal("SQL target bypass")
	}
	migration, err := os.ReadFile("../../migrations/20261004101500001_global_directory_owners.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(t.Context(), strings.Split(string(migration), "-- +goose Down")[1]); err == nil {
		t.Fatal("shared owner downgrade accepted")
	}
	if got, err := s.AppBySlug(t.Context(), owner.ID, apps[0].Slug); err != nil || got.ID != apps[0].ID {
		t.Fatalf("failed downgrade changed app: %v", err)
	}
}
func TestGlobalOrganizationPostgresActorDepartureSurvives(t *testing.T) {
	pool, s, authority, owner, org, _ := orgDirectoryFixture(t)
	developer, err := authority.CreateAccount(t.Context(), "developer@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if err = authority.AddOrgMember(t.Context(), org.ID, developer.ID, state.OrgRoleDeveloper, nil); err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateOrgApp(t.Context(), owner.ID, developer.ID, org.Plan, "departure-app", 3)
	if err != nil {
		t.Fatal(err)
	}
	if err = authority.MarkAccountDeletionPending(t.Context(), developer.ID); err != nil {
		t.Fatal(err)
	}
	if err = authority.DeleteAccount(t.Context(), developer.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := s.AppBySlug(t.Context(), owner.ID, app.Slug); err != nil || got.ID != app.ID {
		t.Fatalf("creator deletion lost organization app: %+v %v", got, err)
	}
	if _, err = pool.Exec(t.Context(), "DELETE FROM orgs WHERE id=$1", org.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AppBySlug(t.Context(), owner.ID, app.Slug); !errors.Is(err, ErrNotFound) {
		t.Fatalf("organization deletion retained app: %v", err)
	}
}
func TestGlobalOrganizationPostgresAdmissionRechecksChanges(t *testing.T) {
	for _, tc := range []struct {
		name, change string
		want         error
	}{{"role", "UPDATE org_memberships SET role='viewer' WHERE org_id=$1 AND account_id=$2", ErrOwnerForbidden}, {"plan", "UPDATE orgs SET plan='free' WHERE id=$1", ErrOwnerPlanChanged}, {"status", "UPDATE orgs SET status='suspended' WHERE id=$1", ErrOwnerInactive}} {
		t.Run(tc.name, func(t *testing.T) {
			pool, s, authority, owner, org, _ := orgDirectoryFixture(t)
			actor, err := authority.CreateAccount(t.Context(), "actor@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			if err = authority.AddOrgMember(t.Context(), org.ID, actor.ID, state.OrgRoleDeveloper, nil); err != nil {
				t.Fatal(err)
			}
			tx := beginDirectoryTx(t, pool)
			if _, err = tx.Exec(t.Context(), "SELECT id FROM orgs WHERE id=$1 FOR UPDATE", org.ID); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			result := make(chan error, 1)
			go func() {
				_, err := s.CreateOrgApp(ctx, owner.ID, actor.ID, org.Plan, "racing-org-app", 3)
				result <- err
			}()
			deadline := time.Now().Add(5 * time.Second)
			blocked := false
			for time.Now().Before(deadline) {
				if err = pool.QueryRow(t.Context(), "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%create_global_org_app%')").Scan(&blocked); err != nil {
					t.Fatal(err)
				}
				if blocked {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if !blocked {
				t.Fatal("admission did not reach organization lock")
			}
			args := []any{org.ID}
			if tc.name == "role" {
				args = append(args, actor.ID)
			}
			if _, err = tx.Exec(t.Context(), tc.change, args...); err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err = <-result; !errors.Is(err, tc.want) {
				t.Fatalf("stale admission accepted: %v", err)
			}
			if apps, err := s.ListApps(t.Context(), owner.ID); err != nil || len(apps) != 0 {
				t.Fatalf("partial intent: %+v %v", apps, err)
			}
		})
	}
}
func TestGlobalOrganizationPersonalMigrationRoundTrip(t *testing.T) {
	pool, s, owner, app, _, binding := directoryPGTargetFixture(t)
	target, err := s.AttachTarget(t.Context(), owner, app.Slug, binding)
	if err != nil {
		t.Fatal(err)
	}
	release, err := s.PlanRelease(t.Context(), owner, app.Slug, uuid.NewString(), releaseTestImage)
	if err != nil {
		t.Fatal(err)
	}
	keys := delegationKeys(t)
	grant, err := keys.Mint(app, target)
	if err != nil {
		t.Fatal(err)
	}
	grant, err = s.EnsureDelegation(t.Context(), owner, app.Slug, grant)
	if err != nil {
		t.Fatal(err)
	}
	token, err := keys.Open(grant)
	if err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../../migrations/20261004101500001_global_directory_owners.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(migration), "-- +goose Down")
	// Roll back later binding triggers before their ADR-483 dependencies.
	bindings, err := os.ReadFile("../../migrations/20261004101500003_global_regional_organization_bindings.sql")
	if err != nil {
		t.Fatal(err)
	}
	bindingParts := strings.Split(string(bindings), "-- +goose Down")
	if _, err = pool.Exec(t.Context(), bindingParts[1]); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(t.Context(), parts[1]); err != nil {
		t.Fatal(err)
	}
	var oldOwner string
	if err = pool.QueryRow(t.Context(), "SELECT account_id::text FROM global_apps WHERE id=$1", app.ID).Scan(&oldOwner); err != nil || oldOwner != owner {
		t.Fatalf("legacy owner: %q %v", oldOwner, err)
	}
	if _, err = pool.Exec(t.Context(), parts[0]); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(t.Context(), bindingParts[0]); err != nil {
		t.Fatal(err)
	}
	if got, err := s.AppBySlug(t.Context(), owner, app.Slug); err != nil || got.ID != app.ID {
		t.Fatalf("app identity changed: %+v %v", got, err)
	}
	if got, err := s.GetRelease(t.Context(), owner, app.Slug, release.ReleaseID); err != nil || got.Generation != release.Generation {
		t.Fatalf("release identity changed: %+v %v", got, err)
	}
	recovered, err := s.ReadDelegation(t.Context(), owner, app.ID, grant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := keys.Open(recovered); err != nil || got != token {
		t.Fatalf("credential changed: %v", err)
	}
}
