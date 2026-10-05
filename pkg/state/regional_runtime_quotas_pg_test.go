//go:build !no_pg

package state

// adr: 614
import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/migrations"
)

func regionalQuotaMigration(t *testing.T) (string, string) {
	t.Helper()
	b, err := migrations.FS.ReadFile("20261004143000001_regional_runtime_quota_journal.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, ok := strings.Cut(string(b), "-- +goose Down")
	if !ok {
		t.Fatal("missing quota downgrade")
	}
	return up, down
}

func TestPGRegionalRuntimeQuotaContract(t *testing.T) {
	s, _, cell, account := releasePGFixture(t)
	runRegionalRuntimeQuotaContract(t, s, s, account, cell)
}

func TestPGRegionalRuntimeQuotaRetentionAndRestart(t *testing.T) {
	s, pool, cell, account := releasePGFixture(t)
	f := newRegionalQuotaFixture(t, s, s, account, cell)
	envelope := f.grantEnvelope(t, f.grant)
	first, err := f.authority.AcceptGrant(t.Context(), envelope)
	if err != nil {
		t.Fatal(err)
	}
	// A fresh store has no in-memory authority; all original claims and signatures
	// are reconstructed from PostgreSQL before retirement.
	restarted := NewPgStore(pool)
	f.authority.Store = restarted
	recovered, err := restarted.RegionalRuntimeQuota(t.Context(), f.grant.Binding.RegionalAppID, f.grant.AllocationID)
	if err != nil || recovered.Grant != first.Grant || !recovered.CreatedAt.Equal(first.CreatedAt) || *recovered.GrantEnvelope != envelope {
		t.Fatalf("restart: %+v %v", recovered, err)
	}
	if _, err = pool.Exec(t.Context(), "DELETE FROM apps WHERE id=$1", f.grant.Binding.RegionalAppID); err != nil {
		t.Fatal(err)
	}
	retirement, retireEnvelope := f.retirement(t, f.grant)
	if _, err = f.authority.AcceptRetirement(t.Context(), retireEnvelope); err != nil {
		t.Fatalf("retirement after collection: %v", err)
	}
	recovered, err = NewPgStore(pool).RegionalRuntimeQuota(t.Context(), f.grant.Binding.RegionalAppID, f.grant.AllocationID)
	if err != nil || recovered.Retirement == nil || *recovered.Retirement != retirement {
		t.Fatalf("collected tombstone: %+v %v", recovered, err)
	}
	replay, err := f.authority.AcceptGrant(t.Context(), envelope)
	if err != nil || replay.Retirement == nil || *replay.Retirement != retirement {
		t.Fatalf("collected replay: %+v %v", replay, err)
	}
	unknown := f.grant
	unknown.AllocationID = uuid.NewString()
	unknown.Generation++
	_, delayed := f.retirement(t, unknown)
	if _, err = f.authority.AcceptRetirement(t.Context(), delayed); err != nil {
		t.Fatalf("unknown retirement after collection: %v", err)
	}
}

func TestPGRegionalRuntimeQuotaSQLGuards(t *testing.T) {
	s, pool, cell, account := releasePGFixture(t)
	f := newRegionalQuotaFixture(t, s, s, account, cell)
	_, retirement := f.retirement(t, f.grant)
	if _, err := f.authority.AcceptRetirement(t.Context(), retirement); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`UPDATE regional_runtime_quota_receipts SET generation=generation+1`,
		`UPDATE regional_runtime_quota_receipts SET grant_claims=jsonb_set(grant_claims,'{Concurrency}','3')`,
		`UPDATE regional_runtime_quota_receipts SET grant_envelope='{}'`,
		`UPDATE regional_runtime_quota_receipts SET created_at=created_at+interval '1 second'`,
		`UPDATE regional_runtime_quota_receipts SET retirement=NULL, retirement_envelope=NULL`,
		`UPDATE regional_runtime_quota_receipts SET retirement=jsonb_set(retirement,'{RetirementID}',to_jsonb(gen_random_uuid()::text))`,
		`UPDATE regional_runtime_quota_receipts SET retirement_envelope='{}'`,
		`DELETE FROM regional_runtime_quota_receipts`,
		`TRUNCATE regional_runtime_quota_receipts CASCADE`,
		`UPDATE regional_runtime_quota_heads SET generation=generation+1`,
		`UPDATE regional_runtime_quota_heads SET stream='{}'`,
		`UPDATE regional_runtime_quota_heads SET admission_enabled=true`,
		`DELETE FROM regional_runtime_quota_heads`,
		`TRUNCATE regional_runtime_quota_heads`,
	} {
		if _, err := pool.Exec(t.Context(), statement); err == nil {
			t.Fatalf("SQL guard allowed %s", statement)
		}
	}
	_, down := regionalQuotaMigration(t)
	if _, err := pool.Exec(t.Context(), down); err == nil || !strings.Contains(err.Error(), "cannot downgrade") {
		t.Fatalf("populated downgrade: %v", err)
	}
	head, err := s.RegionalRuntimeQuotaHead(t.Context(), f.grant.Binding.RegionalAppID)
	if err != nil || head.Grant != f.grant || head.AdmissionEnabled {
		t.Fatalf("guard changed authority: %+v %v", head, err)
	}
}

func TestPGRegionalRuntimeQuotaRollback(t *testing.T) {
	s, pool, cell, account := releasePGFixture(t)
	f := newRegionalQuotaFixture(t, s, s, account, cell)
	if _, err := pool.Exec(t.Context(), `CREATE FUNCTION reject_quota_head_test() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected head failure'; END $$;
 CREATE TRIGGER zz_reject_quota_head_test BEFORE INSERT ON regional_runtime_quota_heads FOR EACH ROW EXECUTE FUNCTION reject_quota_head_test();`); err != nil {
		t.Fatal(err)
	}
	envelope := f.grantEnvelope(t, f.grant)
	if _, err := f.authority.AcceptGrant(t.Context(), envelope); err == nil || !strings.Contains(err.Error(), "injected head failure") {
		t.Fatalf("injected error: %v", err)
	}
	if _, err := s.RegionalRuntimeQuota(t.Context(), f.grant.Binding.RegionalAppID, f.grant.AllocationID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("partial receipt: %v", err)
	}
	if _, err := s.RegionalRuntimeQuotaHead(t.Context(), f.grant.Binding.RegionalAppID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("partial head: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `DROP TRIGGER zz_reject_quota_head_test ON regional_runtime_quota_heads`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.authority.AcceptGrant(t.Context(), envelope); err != nil {
		t.Fatalf("rollback poisoned retry: %v", err)
	}
}

func TestPGRegionalRuntimeQuotaEmptyMigrationRoundTrip(t *testing.T) {
	_, pool, _, _ := releasePGFixture(t)
	up, down := regionalQuotaMigration(t)
	capacityUp, capacityDown := regionalCapacityMigration(t)
	for _, sql := range []string{capacityDown, down, up, capacityUp} {
		if _, err := pool.Exec(t.Context(), sql); err != nil {
			t.Fatalf("empty quota migration round trip: %v", err)
		}
	}
}

func TestPGRegionalRuntimeQuotaOwnerLockWait(t *testing.T) {
	s, pool, cell, account := releasePGFixture(t)
	f := newRegionalQuotaFixture(t, s, s, account, cell)
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err = tx.Exec(t.Context(), "UPDATE apps SET slug='changed-quota-app' WHERE id=$1", f.grant.Binding.RegionalAppID); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	envelope := f.grantEnvelope(t, f.grant)
	go func() { _, acceptErr := f.authority.AcceptGrant(t.Context(), envelope); result <- acceptErr }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var blocked bool
		if err = pool.QueryRow(t.Context(), "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%LockRegionalRuntimeQuotaApp%')").Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("quota writer did not wait on app identity")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = <-result; !errors.Is(err, ErrNotFound) {
		t.Fatalf("changed slug accepted after lock wait: %v", err)
	}
	if _, err = s.RegionalRuntimeQuotaHead(t.Context(), f.grant.Binding.RegionalAppID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("identity failure persisted head: %v", err)
	}
}

func TestPGRegionalRuntimeQuotaTenantLockWait(t *testing.T) {
	s, pool, cell, account := releasePGFixture(t)
	app, org, _ := quotaSharedApp(t, s, account, cell)
	f := newRegionalQuotaFixture(t, s, s, account, cell)
	f.grant.Binding.RegionalOrgID = org.ID
	f.grant.Binding.RegionalAppID, f.grant.Binding.RegionalAppSlug = app.ID, app.Slug
	f.authority.Binding = f.grant.Binding
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err = tx.Exec(t.Context(), "UPDATE orgs SET deleted_pending=true WHERE id=$1", org.ID); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	envelope := f.grantEnvelope(t, f.grant)
	go func() { _, acceptErr := f.authority.AcceptGrant(t.Context(), envelope); result <- acceptErr }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var blocked bool
		if err = pool.QueryRow(t.Context(), "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%LockRegionalRuntimeQuotaTenant%')").Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("quota writer did not wait on tenant identity")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = <-result; !errors.Is(err, ErrNotFound) {
		t.Fatalf("inactive tenant accepted after lock wait: %v", err)
	}
	if _, err = s.RegionalRuntimeQuotaHead(t.Context(), app.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("tenant failure persisted head: %v", err)
	}
}
