//go:build !no_pg

package globaldirectory

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestPostgresDelegationContract(t *testing.T) {
	pool := directoryPG(t)
	s := NewPgStore(pool)
	delegationContract(t, s, s, directoryAccount(t, pool))
}
func TestPostgresDelegationDeletionOutboxAndGuards(t *testing.T) {
	pool := directoryPG(t)
	s := NewPgStore(pool)
	owner := directoryAccount(t, pool)
	app, targets := releaseSetup(t, s, owner)
	keys := delegationKeys(t)
	g, err := keys.Mint(app, targets[0])
	if err != nil {
		t.Fatal(err)
	}
	g, err = s.EnsureDelegation(t.Context(), owner, app.Slug, g)
	if err != nil {
		t.Fatal(err)
	}
	queries := []string{
		"UPDATE global_release_delegations SET api_base='https://attacker.example.com' WHERE id=$1",
		"UPDATE global_release_delegations SET token_sha256=decode(repeat('00',32),'hex') WHERE id=$1",
		"UPDATE global_release_delegations SET status='revoked',sealed_credential=sealed_credential WHERE id=$1",
		"DELETE FROM global_release_delegations WHERE id=$1",
	}
	for _, query := range queries {
		_, err := pool.Exec(t.Context(), query, g.ID)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
			t.Fatalf("SQL guard %s: %v", query, err)
		}
	}
	if _, err := pool.Exec(t.Context(), "INSERT INTO global_release_recovery_work(global_app_id,account_id,global_app_slug) VALUES($1,$2,'forged-app')", uuid.NewString(), owner); err == nil {
		t.Fatal("forged recovery identity")
	}
	if _, err := pool.Exec(t.Context(), "DELETE FROM accounts WHERE id=$1", owner); err != nil {
		t.Fatal(err)
	}
	retained, err := s.ReadDelegation(t.Context(), owner, app.ID, g.ID)
	if err != nil || retained.Status != DelegationRevoking || len(retained.Sealed) == 0 {
		t.Fatalf("owner deletion lost outbox: %+v %v", retained, err)
	}
	if _, err := keys.Open(retained); err != nil {
		t.Fatal(err)
	}
	work, err := s.ClaimRecovery(t.Context(), uuid.NewString())
	if err != nil || len(work) != 1 || work[0].GlobalAppID != app.ID || work[0].ReleaseID != "" {
		t.Fatalf("deleted owner work: %+v %v", work, err)
	}
	if _, err := pool.Exec(t.Context(), "DELETE FROM global_release_recovery_work WHERE global_app_id=$1", app.ID); err == nil {
		t.Fatal("live outbox removed")
	}
	if err := s.SetDelegationStatus(t.Context(), owner, app.ID, g.ID, DelegationRevoked); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishRecovery(t.Context(), work[0]); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresDelegationQuotaIncludesRevocation(t *testing.T) {
	pool := directoryPG(t)
	s := NewPgStore(pool)
	owner := directoryAccount(t, pool)
	app, targets := releaseSetup(t, s, owner)
	keys := delegationKeys(t)
	g, err := keys.Mint(app, targets[0])
	if err != nil {
		t.Fatal(err)
	}
	g, err = s.EnsureDelegation(t.Context(), owner, app.Slug, g)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetDelegationStatus(t.Context(), owner, app.ID, g.ID, DelegationRevoking); err != nil {
		t.Fatal(err)
	}
	// The bulk fixture exercises the database count under the same app lock.
	_, err = pool.Exec(t.Context(), `DO $$ DECLARE i integer; new_id uuid; BEGIN
 FOR i IN 2..1024 LOOP
  new_id=gen_random_uuid();
  INSERT INTO global_release_delegations(id,global_app_id,account_id,global_app_slug,cell_id,region,cell_generation,api_base,regional_account_id,regional_app_id,regional_app_slug,token_sha256,sealed_credential,expires_at)
   SELECT new_id,global_app_id,account_id,global_app_slug,cell_id,region,cell_generation,api_base,regional_account_id,regional_app_id,regional_app_slug,token_sha256,sealed_credential,expires_at
    FROM global_release_delegations ORDER BY created_at LIMIT 1;
  UPDATE global_release_delegations SET status='revoking' WHERE id=new_id;
 END LOOP;
 END $$`)
	if err != nil {
		t.Fatal(err)
	}
	next, err := keys.Mint(app, targets[0])
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.EnsureDelegation(t.Context(), owner, app.Slug, next)
	var bound *DelegationLimitError
	if !errors.As(err, &bound) || bound.Limit != 1024 || bound.Observed != 1025 {
		t.Fatalf("revocation quota: %v", err)
	}
	if err := s.SetDelegationStatus(t.Context(), owner, app.ID, g.ID, DelegationRevoked); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnsureDelegation(t.Context(), owner, app.Slug, next); err != nil {
		t.Fatalf("terminal quota release: %v", err)
	}
}

func TestPostgresDelegationExpiryErasesCipher(t *testing.T) {
	pool := directoryPG(t)
	s := NewPgStore(pool)
	owner := directoryAccount(t, pool)
	app, targets := releaseSetup(t, s, owner)
	keys := delegationKeys(t)
	g, err := keys.Mint(app, targets[0])
	if err != nil {
		t.Fatal(err)
	}
	token, err := keys.Open(g)
	if err != nil {
		t.Fatal(err)
	}
	g.ExpiresAt = time.Now().UTC().Add(250 * time.Millisecond).Truncate(time.Microsecond)
	g.Sealed, err = keys.Seal(g, token)
	if err != nil {
		t.Fatal(err)
	}
	g, err = s.EnsureDelegation(t.Context(), owner, app.Slug, g)
	if err != nil {
		t.Fatal(err)
	}
	timer := time.NewTimer(time.Until(g.ExpiresAt) + 20*time.Millisecond)
	defer timer.Stop()
	select {
	case <-t.Context().Done():
		t.Fatal(t.Context().Err())
	case <-timer.C:
	}
	if err := s.SetDelegationStatus(t.Context(), owner, app.ID, g.ID, DelegationExpired); err != nil {
		t.Fatal(err)
	}
	g, err = s.ReadDelegation(t.Context(), owner, app.ID, g.ID)
	if err != nil || len(g.Sealed) != 0 || g.Status != DelegationExpired {
		t.Fatalf("expiry: %+v %v", g, err)
	}
	if err := s.SetDelegationStatus(t.Context(), owner, app.ID, g.ID, DelegationActive); err == nil {
		t.Fatal("expired consent revived")
	}
}
