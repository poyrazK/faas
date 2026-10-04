//go:build !no_pg

package state

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestPGRegionalReleaseGrantContract(t *testing.T) {
	s, _, cell, acct := releasePGFixture(t)
	runRegionalGrantContract(t, s, s, cell, acct)
}
func TestPGRegionalReleaseGrantBound(t *testing.T) {
	s, _, cell, acct := releasePGFixture(t)
	runRegionalGrantBound(t, s, s, cell, acct)
}
func TestPGRegionalReleaseGrantGuards(t *testing.T) {
	s, pool, cell, acct := releasePGFixture(t)
	app := releaseTestApp(t, s, acct)
	g := grantFixture(t, app, cell)
	if _, err := s.PutRegionalReleaseGrant(t.Context(), g); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"UPDATE regional_release_grants SET token_sha256=decode(repeat('ab',32),'hex')",
		"UPDATE regional_release_grants SET expires_at=expires_at+interval '1 second'",
		"DELETE FROM regional_release_grants",
		"TRUNCATE regional_release_grants",
	} {
		_, err := pool.Exec(t.Context(), statement)
		var p *pgconn.PgError
		if !errors.As(err, &p) || p.ConstraintName != "regional_grant_identity" {
			t.Fatalf("guard %s: %v", statement, err)
		}
	}
	if _, err := s.RevokeRegionalReleaseGrant(t.Context(), acct.ID, cell, app.ID, g.ID); err != nil {
		t.Fatal(err)
	}
	_, err := pool.Exec(t.Context(), "UPDATE regional_release_grants SET status='active',revoked_at=NULL")
	var p *pgconn.PgError
	if !errors.As(err, &p) || p.ConstraintName != "regional_grant_identity" {
		t.Fatal("revival guard", err)
	}
	migration, err := os.ReadFile("../../migrations/20261004033000528_regional_release_grants.sql")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(t.Context(), strings.Split(string(migration), "-- +goose Down\n")[1])
	_ = tx.Rollback(t.Context())
	if err == nil || !strings.Contains(err.Error(), "cannot downgrade populated") {
		t.Fatal("downgrade", err)
	}
}
func TestPGRegionalReleaseGrantExpiryAndOwner(t *testing.T) {
	s, pool, cell, acct := releasePGFixture(t)
	app := releaseTestApp(t, s, acct)
	g := grantFixture(t, app, cell)
	g.ExpiresAt = time.Now().UTC().Add(time.Second).Truncate(time.Microsecond)
	if _, err := s.PutRegionalReleaseGrant(t.Context(), g); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Until(g.ExpiresAt) + 10*time.Millisecond)
	if _, _, err := s.AuthenticateRegionalReleaseGrant(t.Context(), g.Hash); !errors.Is(err, ErrAPIKeyExpired) {
		t.Fatal("expired auth", err)
	}
	intent, dep := releaseTestIntent(cell, app)
	intent.GrantID, intent.GlobalAppID, intent.Generation = g.ID, g.GlobalAppID, 1
	if _, err := s.CreateRegionalImageRelease(t.Context(), intent, dep, nil); !errors.Is(err, ErrAPIKeyExpired) {
		t.Fatal("expired acceptance", err)
	}
	live := grantFixture(t, app, cell)
	if _, err := s.PutRegionalReleaseGrant(t.Context(), live); err != nil {
		t.Fatal(err)
	}
	foreign, err := s.CreateAccount(t.Context(), "foreign-grant@example.com", acct.Plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE apps SET account_id=$1,org_id=NULL WHERE id=$2", foreign.ID, app.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AuthenticateRegionalReleaseGrant(t.Context(), live.Hash); !errors.Is(err, ErrNotFound) {
		t.Fatal("transferred owner", err)
	}
}

func TestPGRegionalReleaseGrantRevocationLocksAppFirst(t *testing.T) {
	s, pool, cell, acct := releasePGFixture(t)
	app := releaseTestApp(t, s, acct)
	g := grantFixture(t, app, cell)
	if _, err := s.PutRegionalReleaseGrant(t.Context(), g); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err := tx.Exec(t.Context(), "SELECT id FROM apps WHERE id=$1 FOR UPDATE", app.ID); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := s.RevokeRegionalReleaseGrant(ctx, acct.ID, cell, app.ID, g.ID); result <- err }()
	blocked := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%lock_regional_release_grant_app%')`).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("revocation did not wait for app lock")
	}
	if _, err := tx.Exec(ctx, "SELECT id FROM regional_release_grants WHERE id=$1 FOR UPDATE NOWAIT", g.ID); err != nil {
		t.Fatal("grant locked before app", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}
