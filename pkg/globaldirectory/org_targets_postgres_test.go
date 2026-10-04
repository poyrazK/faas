//go:build !no_pg

package globaldirectory

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestGlobalOrganizationTargetPostgresTenantClaimRace(t *testing.T) {
	pool, s, authority, root, org, actor := orgDirectoryFixture(t)
	otherOrg, err := authority.CreateOrg(t.Context(), state.Org{Slug: "claim-other", Name: "Other Team", Plan: org.Plan})
	if err != nil {
		t.Fatal(err)
	}
	if err = authority.AddOrgMember(t.Context(), otherOrg.ID, actor.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	other, err := s.ResolveOrgOwner(t.Context(), otherOrg.ID)
	if err != nil {
		t.Fatal(err)
	}
	owners := []Owner{root, other}
	slugs := []string{"claim-first", "claim-second"}
	for i, o := range owners {
		if _, err = s.CreateOrgApp(t.Context(), o.ID, actor.ID, org.Plan, slugs[i], 3); err != nil {
			t.Fatal(err)
		}
	}
	cell, err := s.PutCell(t.Context(), Cell{ID: uuid.NewString(), Region: "eu-fsn", APIBase: "https://fsn.example.com", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	b := Binding{CellID: cell.ID, Region: cell.Region, CellGeneration: 1, RegionalAccountID: uuid.NewString(), RegionalOrgID: uuid.NewString(), RegionalAppID: uuid.NewString(), RegionalAppSlug: "regional-team"}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i, o := range owners {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := s.AttachOrgTarget(t.Context(), o.ID, actor.ID, slugs[i], b); e == nil {
				wins.Add(1)
			} else if !errors.Is(e, ErrConflict) {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("tenant claim winners: %d", wins.Load())
	}
	var n int
	if err = pool.QueryRow(t.Context(), "SELECT count(*) FROM global_owner_cell_bindings WHERE cell_id=$1", cell.ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("partial loser mapping: %d %v", n, err)
	}
}

func TestGlobalOrganizationTargetPostgresContract(t *testing.T) {
	_, s, a, owner, org, actor := orgDirectoryFixture(t)
	exerciseOrgTargets(t, s, a, owner, org, actor)
}

func TestGlobalOrganizationTargetPostgresAuthorityWait(t *testing.T) {
	for _, kind := range []string{"role", "removal", "org", "actor"} {
		t.Run(kind, func(t *testing.T) {
			pool, s, a, owner, org, creator := orgDirectoryFixture(t)
			actor, err := a.CreateAccount(t.Context(), "binding-actor@example.com", org.Plan)
			if err != nil {
				t.Fatal(err)
			}
			if err = a.AddOrgMember(t.Context(), org.ID, actor.ID, state.OrgRoleAdmin, nil); err != nil {
				t.Fatal(err)
			}
			app, err := s.CreateOrgApp(t.Context(), owner.ID, creator.ID, org.Plan, "waiting-binding", 3)
			if err != nil {
				t.Fatal(err)
			}
			cell, err := s.PutCell(t.Context(), Cell{ID: uuid.NewString(), Region: "eu-fsn", APIBase: "https://fsn.example.com", Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			b := Binding{CellID: cell.ID, Region: cell.Region, CellGeneration: 1, RegionalAccountID: uuid.NewString(), RegionalOrgID: uuid.NewString(), RegionalAppID: uuid.NewString(), RegionalAppSlug: "regional-team"}
			tx := beginDirectoryTx(t, pool)
			if _, err = tx.Exec(t.Context(), "SELECT id FROM orgs WHERE id=$1 FOR UPDATE", org.ID); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			result := make(chan error, 1)
			go func() { _, err := s.AttachOrgTarget(ctx, owner.ID, actor.ID, app.Slug, b); result <- err }()
			blocked := false
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				if err = pool.QueryRow(t.Context(), "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%lock_global_org_target_owner%')").Scan(&blocked); err != nil {
					t.Fatal(err)
				}
				if blocked {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if !blocked {
				t.Fatal("binding did not reach authority lock")
			}
			q := "UPDATE org_memberships SET role='viewer' WHERE org_id=$1 AND account_id=$2"
			args := []any{org.ID, actor.ID}
			want := ErrOwnerForbidden
			switch kind {
			case "removal":
				q = "UPDATE org_memberships SET removed_at=now() WHERE org_id=$1 AND account_id=$2"
			case "org":
				q = "UPDATE orgs SET status='suspended' WHERE id=$1"
				args = []any{org.ID}
				want = ErrOwnerInactive
			case "actor":
				q = "UPDATE accounts SET status='deleted_pending' WHERE id=$1"
				args = []any{actor.ID}
			}
			if _, err = tx.Exec(t.Context(), q, args...); err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err = <-result; !errors.Is(err, want) {
				t.Fatalf("stale binding accepted: %v", err)
			}
			var count int
			if err = pool.QueryRow(t.Context(), "SELECT count(*) FROM global_owner_cell_bindings WHERE owner_id=$1", owner.ID).Scan(&count); err != nil || count != 0 {
				t.Fatalf("partial mapping: %d %v", count, err)
			}
		})
	}
}

func TestGlobalOrganizationTargetPostgresSQLAndRollback(t *testing.T) {
	pool, s, _, owner, org, actor := orgDirectoryFixture(t)
	app, err := s.CreateOrgApp(t.Context(), owner.ID, actor.ID, org.Plan, "guarded-binding", 3)
	if err != nil {
		t.Fatal(err)
	}
	cell, err := s.PutCell(t.Context(), Cell{ID: uuid.NewString(), Region: "eu-fsn", APIBase: "https://fsn.example.com", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	b := Binding{CellID: cell.ID, Region: cell.Region, CellGeneration: 1, RegionalAccountID: uuid.NewString(), RegionalOrgID: uuid.NewString(), RegionalAppID: uuid.NewString(), RegionalAppSlug: "regional-team"}
	if _, err = s.AttachOrgTarget(t.Context(), owner.ID, actor.ID, app.Slug, b); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"UPDATE global_owner_cell_bindings SET regional_org_id=gen_random_uuid() WHERE owner_id=$1", "DELETE FROM global_owner_cell_bindings WHERE owner_id=$1", "UPDATE global_app_targets SET regional_org_id=gen_random_uuid() WHERE owner_id=$1", "INSERT INTO global_image_releases(global_app_id,release_id,generation,image_ref) SELECT id,gen_random_uuid(),1,'test@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa' FROM global_apps WHERE owner_id=$1"} {
		if _, err = pool.Exec(t.Context(), q, owner.ID); err == nil {
			t.Fatalf("SQL bypass: %s", q)
		}
	}
	m, err := os.ReadFile("../../migrations/20261004101500003_global_regional_organization_bindings.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(t.Context(), strings.Split(string(m), "-- +goose Down")[1]); err == nil {
		t.Fatal("shared downgrade accepted")
	}
	if got, err := Resolve(t.Context(), s, owner.ID, app.Slug, b.Region); err != nil || got.Binding != b {
		t.Fatalf("failed downgrade changed binding: %+v %v", got, err)
	}
}

func TestGlobalOrganizationTargetPersonalMigrationRoundTrip(t *testing.T) {
	pool, s, owner, app, _, b := directoryPGTargetFixture(t)
	target, err := s.AttachTarget(t.Context(), owner, app.Slug, b)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.PlanRelease(t.Context(), owner, app.Slug, uuid.NewString(), releaseTestImage)
	if err != nil {
		t.Fatal(err)
	}
	keys := delegationKeys(t)
	g, err := keys.Mint(app, target)
	if err != nil {
		t.Fatal(err)
	}
	g, err = s.EnsureDelegation(t.Context(), owner, app.Slug, g)
	if err != nil {
		t.Fatal(err)
	}
	token, err := keys.Open(g)
	if err != nil {
		t.Fatal(err)
	}
	m, err := os.ReadFile("../../migrations/20261004101500003_global_regional_organization_bindings.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(m), "-- +goose Down")
	if _, err = pool.Exec(t.Context(), parts[1]); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(t.Context(), parts[0]); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetRelease(t.Context(), owner, app.Slug, r.ReleaseID)
	if err != nil || got.Targets[0].Binding != b {
		t.Fatalf("release round trip: %+v %v", got, err)
	}
	g, err = s.CurrentDelegation(t.Context(), owner, app.Slug, b.Region)
	if err != nil {
		t.Fatal(err)
	}
	if retry, err := keys.Open(g); err != nil || retry != token {
		t.Fatalf("ciphertext round trip: %v", err)
	}
}
