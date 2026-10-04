//go:build !no_pg

package state

// adr: 484
import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/migrations"
)

func TestPGRegionalOrganizationGrantContract(t *testing.T) {
	s, _, cell, creator := releasePGFixture(t)
	runRegionalOrganizationGrantContract(t, s, cell, creator)
}

func TestPGRegionalOrganizationGrantAuthorityAfterLockWait(t *testing.T) {
	for _, tc := range []struct{ name, change string }{
		{"role", "UPDATE org_memberships SET role='viewer' WHERE org_id=$1 AND account_id=$2"},
		{"removal", "UPDATE org_memberships SET removed_at=clock_timestamp() WHERE org_id=$1 AND account_id=$2"},
		{"org", "UPDATE orgs SET status='suspended' WHERE id=$1 AND $2::uuid IS NOT NULL"},
		{"actor", "UPDATE accounts SET status='suspended' WHERE id=$2 AND $1::uuid IS NOT NULL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, pool, cell, creator := releasePGFixture(t)
			_, org, issuer, g := sharedRegionalGrantFixture(t, s, creator, cell)
			tx, err := pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(t.Context()) }()
			if _, err = tx.Exec(t.Context(), "SELECT id FROM orgs WHERE id=$1 FOR UPDATE", org.ID); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			result := make(chan error, 1)
			go func() { _, err := s.PutRegionalReleaseGrant(ctx, g); result <- err }()
			blocked := false
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				if err = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%lock_regional_release_grant_app%')`).Scan(&blocked); err != nil {
					t.Fatal(err)
				}
				if blocked {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if !blocked {
				t.Fatal("registration did not wait for authority lock")
			}
			if _, err = tx.Exec(ctx, tc.change, org.ID, issuer.ID); err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err = <-result; !errors.Is(err, ErrNotFound) {
				t.Fatal("stale actor admitted", err)
			}
			var count int
			if err = pool.QueryRow(ctx, "SELECT count(*) FROM regional_release_grants WHERE id=$1", g.ID).Scan(&count); err != nil || count != 0 {
				t.Fatalf("rejected consent retained: %d %v", count, err)
			}
		})
	}
}

func regionalOrganizationGrantMigration(t *testing.T) (string, string) {
	t.Helper()
	b, err := migrations.FS.ReadFile("20261004101500002_regional_organization_grant_issuers.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, ok := strings.Cut(string(b), "-- +goose Down\n")
	if !ok {
		t.Fatal("missing down migration")
	}
	return up, down
}

func TestPGRegionalOrganizationGrantGuardsAndIssuerDeletion(t *testing.T) {
	s, pool, cell, creator := releasePGFixture(t)
	app, _, issuer, g := sharedRegionalGrantFixture(t, s, creator, cell)
	if _, err := s.PutRegionalReleaseGrant(t.Context(), g); err != nil {
		t.Fatal(err)
	}
	_, err := pool.Exec(t.Context(), "UPDATE regional_release_grants SET issuer_account_id=$1 WHERE id=$2", creator.ID, g.ID)
	var p *pgconn.PgError
	if !errors.As(err, &p) || p.ConstraintName != "regional_grant_identity" {
		t.Fatal("issuer rewrite", err)
	}
	_, down := regionalOrganizationGrantMigration(t)
	if _, err = pool.Exec(t.Context(), down); err == nil || !strings.Contains(err.Error(), "cannot downgrade organization") {
		t.Fatal("shared downgrade", err)
	}
	if _, err = pool.Exec(t.Context(), "DELETE FROM accounts WHERE id=$1", issuer.ID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = pool.QueryRow(t.Context(), "SELECT count(*) FROM regional_release_grants WHERE id=$1 AND issuer_account_id=$2", g.ID, issuer.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("issuer deletion erased identity: %d %v", count, err)
	}
	if _, _, err = s.AuthenticateRegionalReleaseGrant(t.Context(), g.Hash); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted issuer authenticated", err)
	}
	if revoked, err := s.RevokeRegionalReleaseGrant(t.Context(), creator.ID, cell, app.ID, g.ID); err != nil || revoked.Status != "revoked" {
		t.Fatalf("orphan consent revoke: %+v %v", revoked, err)
	}
}

func TestPGRegionalOrganizationGrantPersonalMigrationRoundTrip(t *testing.T) {
	s, pool, cell, acct := releasePGFixture(t)
	app := releaseTestApp(t, s, acct)
	g := grantFixture(t, app, cell)
	first, err := s.PutRegionalReleaseGrant(t.Context(), g)
	if err != nil {
		t.Fatal(err)
	}
	up, down := regionalOrganizationGrantMigration(t)
	if _, err = pool.Exec(t.Context(), down); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(t.Context(), up); err != nil {
		t.Fatal(err)
	}
	retry, err := s.PutRegionalReleaseGrant(t.Context(), g)
	if err != nil || !retry.matches(first) || !retry.CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf("personal round trip: %+v %v", retry, err)
	}
	if got, key, err := s.AuthenticateRegionalReleaseGrant(t.Context(), g.Hash); err != nil || got.ID != acct.ID || key.AccountID != acct.ID {
		t.Fatalf("personal identity after migration: %+v %+v %v", got, key, err)
	}
}

func TestPGRegionalOrganizationGrantRawInsertAuthority(t *testing.T) {
	s, pool, cell, creator := releasePGFixture(t)
	app, org, issuer, g := sharedRegionalGrantFixture(t, s, creator, cell)
	if err := s.UpdateOrgMemberRole(t.Context(), org.ID, issuer.ID, OrgRoleViewer); err != nil {
		t.Fatal(err)
	}
	_, err := pool.Exec(t.Context(), `INSERT INTO regional_release_grants(id,app_id,cell_id,account_id,issuer_account_id,org_id,global_app_id,token_sha256,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, g.ID, app.ID, cell, creator.ID, issuer.ID, org.ID, g.GlobalAppID, g.Hash, g.ExpiresAt)
	var p *pgconn.PgError
	if !errors.As(err, &p) || p.Code != "P0002" {
		t.Fatal("raw insert bypassed current role", err)
	}
}

func TestPGRegionalOrganizationGrantBound(t *testing.T) {
	s, _, cell, creator := releasePGFixture(t)
	runRegionalOrganizationGrantBound(t, s, cell, creator)
}
