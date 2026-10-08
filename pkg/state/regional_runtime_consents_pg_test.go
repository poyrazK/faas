//go:build !no_pg

package state

// adr: 773
import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPGRuntimeConsentContract(t *testing.T) {
	s, _, cell, account := releasePGFixture(t)
	runRuntimeConsentContract(t, s, s, s, account, cell)
}

func TestPGRuntimeConsentRetainsOriginalSourceAndGuards(t *testing.T) {
	s, pool, cell, account := releasePGFixture(t)
	f, c := runtimeConsentFixture(t, s, s, account, cell)
	if _, err := s.PutRegionalRuntimeConsent(t.Context(), c); err != nil {
		t.Fatal(err)
	}
	if _, err := f.authority.AcceptDelegatedGrant(t.Context(), f.grantEnvelope(t, f.grant), c.ID); err != nil {
		t.Fatal(err)
	}
	f.authority.Store = NewPgStore(pool)
	receipt, err := f.authority.Store.RegionalRuntimeQuota(t.Context(), c.Binding.RegionalAppID, f.grant.AllocationID)
	if err != nil || receipt.ConsentID != c.ID {
		t.Fatal("restart lost original consent source", err)
	}
	for _, statement := range []string{
		`UPDATE regional_runtime_consents SET issuer_id=gen_random_uuid()`,
		`UPDATE regional_runtime_consents SET owner_id=gen_random_uuid()`,
		`UPDATE regional_runtime_consents SET expires_at=expires_at+interval '1 second'`,
		`UPDATE regional_runtime_consents SET created_at=created_at+interval '1 second'`,
		`UPDATE regional_runtime_consents SET binding=jsonb_set(binding,'{CellGeneration}','2')`,
		`DELETE FROM regional_runtime_consents`,
		`TRUNCATE regional_runtime_consents CASCADE`,
		`UPDATE regional_runtime_quota_receipts SET consent_id=NULL`,
	} {
		if _, err := pool.Exec(t.Context(), statement); err == nil {
			t.Fatalf("SQL changed retained consent: %s", statement)
		}
	}
	for _, field := range []string{"CellID", "Region", "CellGeneration", "RegionalAccountID", "RegionalOrgID", "RegionalAppID", "RegionalAppSlug", "APIBase"} {
		if _, err := pool.Exec(t.Context(), `INSERT INTO regional_runtime_consents(id,app_id,account_id,cell_id,issuer_id,global_app_id,owner_id,binding,expires_at)
            SELECT gen_random_uuid(),app_id,account_id,cell_id,issuer_id,global_app_id,owner_id,jsonb_set(binding,ARRAY[$1]::text[],'null'::jsonb),expires_at FROM regional_runtime_consents WHERE id=$2`, field, c.ID); err == nil {
			t.Fatalf("SQL accepted null binding field %s", field)
		}
	}
	if _, err := s.RevokeRegionalRuntimeConsent(t.Context(), account.ID, cell, c.Binding.RegionalAppID, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE regional_runtime_consents SET status='active',revoked_at=NULL`); err == nil {
		t.Fatal("SQL resurrected revoked consent")
	}
	b, err := migrations.FS.ReadFile("20261006163449955_regional_runtime_delivery_consents.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, down, ok := strings.Cut(string(b), "-- +goose Down")
	if !ok {
		t.Fatal("missing downgrade")
	}
	if _, err := pool.Exec(t.Context(), down); err == nil || !strings.Contains(err.Error(), "cannot downgrade retained runtime consents") {
		t.Fatal("downgrade discarded consent evidence", err)
	}
	if _, err := pool.Exec(t.Context(), `DELETE FROM apps WHERE id=$1`, c.Binding.RegionalAppID); err != nil {
		t.Fatal(err)
	}
	if _, err := NewPgStore(pool).RegionalRuntimeConsent(t.Context(), account.ID, cell, c.Binding.RegionalAppID, c.ID); err != nil {
		t.Fatal("collection removed original consent evidence", err)
	}
}

func TestPGRuntimeConsentExpiryWinsWaitingOwner(t *testing.T) {
	for _, kind := range []string{"fresh", "replay", "direct-sql"} {
		t.Run(kind, func(t *testing.T) {
			s, pool, cell, account := releasePGFixture(t)
			f, c := runtimeConsentFixture(t, s, s, account, cell)
			c.ExpiresAtMicros = time.Now().Add(3 * time.Second).UnixMicro()
			if _, err := s.PutRegionalRuntimeConsent(t.Context(), c); err != nil {
				t.Fatal(err)
			}
			envelope := f.grantEnvelope(t, f.grant)
			if kind == "replay" {
				if _, err := f.authority.AcceptDelegatedGrant(t.Context(), envelope, c.ID); err != nil {
					t.Fatal(err)
				}
			}
			tx, err := pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = tx.Rollback(t.Context()) })
			var blocker int
			if err := tx.QueryRow(t.Context(), `SELECT pg_backend_pid() FROM accounts WHERE id=$1 FOR UPDATE`, account.ID).Scan(&blocker); err != nil {
				t.Fatal(err)
			}
			params, err := regionalQuotaParams(RegionalRuntimeQuotaReceipt{Grant: f.grant, GrantEnvelope: &envelope, ConsentID: c.ID})
			if err != nil {
				t.Fatal(err)
			}
			result := make(chan error, 1)
			go func() {
				if kind == "direct-sql" {
					_, err := sqlc.New().InsertRegionalRuntimeQuotaReceipt(t.Context(), pool, params)
					result <- err
					return
				}
				_, err := f.authority.AcceptDelegatedGrant(t.Context(), envelope, c.ID)
				result <- err
			}()
			query := "lock_regional_runtime_consent_owner"
			if kind == "direct-sql" {
				query = "INSERT INTO regional_runtime_quota_receipts"
			}
			waitQuotaWriteBlocked(t, pool, blocker, query)
			if wait := time.Until(time.UnixMicro(c.ExpiresAtMicros)); wait > 0 {
				time.Sleep(wait)
			}
			if err := tx.Commit(t.Context()); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-result:
				if err == nil || kind != "direct-sql" && !errors.Is(err, ErrRegionalRuntimeConsent) {
					t.Fatal("owner wait admitted an expired consent", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("waiting owner did not finish")
			}
			_, err = s.RegionalRuntimeQuota(t.Context(), c.Binding.RegionalAppID, f.grant.AllocationID)
			if kind == "replay" && err != nil || kind != "replay" && !errors.Is(err, ErrNotFound) {
				t.Fatal("expired delivery changed original receipt presence", err)
			}
		})
	}
}

func TestPGRuntimeConsentRevokeWinsWaitingDelivery(t *testing.T) {
	s, pool, cell, account := releasePGFixture(t)
	f, c := runtimeConsentFixture(t, s, s, account, cell)
	if _, err := s.PutRegionalRuntimeConsent(t.Context(), c); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(t.Context()) })
	if err := (&sqlc.Queries{}).LockRegionalRuntimeQuotaJournal(t.Context(), tx, uuidToPgtype(c.Binding.RegionalAppID)); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), `UPDATE regional_runtime_consents SET status='revoked',revoked_at=clock_timestamp() WHERE id=$1`, c.ID); err != nil {
		t.Fatal(err)
	}
	envelope := f.grantEnvelope(t, f.grant)
	result := make(chan error, 1)
	go func() {
		_, err := f.authority.AcceptDelegatedGrant(t.Context(), envelope, c.ID)
		result <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		if err := pool.QueryRow(t.Context(), `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%gregale-regional-runtime-quota:%')`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("delivery never reached journal lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, ErrRegionalRuntimeConsent) {
			t.Fatal("waiting delivery ignored committed revoke", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("waiting delivery did not finish")
	}
	if _, err := s.RegionalRuntimeQuota(t.Context(), c.Binding.RegionalAppID, f.grant.AllocationID); !errors.Is(err, ErrNotFound) && !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("rejected delivery mutated journal", err)
	}
}
