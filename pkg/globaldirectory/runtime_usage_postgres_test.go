//go:build !no_pg

package globaldirectory

// adr: 798
import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/state"
)

func runtimeUsageMigrationParts(t *testing.T) []string {
	t.Helper()
	raw, err := migrations.FS.ReadFile("20261011160000001_global_runtime_usage.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(raw), "-- +goose Down")
	if len(parts) != 2 {
		t.Fatal("usage migration sections")
	}
	return parts
}

func TestGlobalRuntimeUsagePostgresContract(t *testing.T) {
	_, q := postgresQuotaFixture(t, false, 2)
	exerciseRuntimeUsageContract(t, newRuntimeUsageFixture(t, q))
}

func TestGlobalRuntimeUsagePostgresAtomicityAndRetention(t *testing.T) {
	pool, q := postgresQuotaFixture(t, false, 2)
	f := newRuntimeUsageFixture(t, q)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `CREATE FUNCTION reject_global_usage_test() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected usage failure'; END $$; CREATE TRIGGER zz_reject_global_usage_test BEFORE INSERT ON financial_usage_evidence FOR EACH ROW EXECUTE FUNCTION reject_global_usage_test();`); err != nil {
		t.Fatal(err)
	}
	u := f.usage(0, f.minute, 520*17)
	e := f.envelope(t, 0, u)
	if _, err := state.ImportGlobalRuntimeUsage(ctx, f.store, f.trust, e, f.now); err == nil || !strings.Contains(err.Error(), "injected usage failure") {
		t.Fatalf("injected rollback: %v", err)
	}
	if _, err := f.store.GlobalRuntimeUsage(ctx, u.Grant.GlobalAppID, u.Grant.AllocationID, u.Grant.Binding.CellID, f.minute); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("partial receipt: %v", err)
	}
	rows, err := q.authority.UsageByHour(ctx, q.owner.AccountID, f.minute, f.minute.Add(time.Minute))
	if err != nil || len(rows) != 0 {
		t.Fatalf("partial canonical billing: %+v %v", rows, err)
	}
	if _, err := pool.Exec(ctx, `DROP TRIGGER zz_reject_global_usage_test ON financial_usage_evidence`); err != nil {
		t.Fatal(err)
	}
	first := f.importUsage(t, 0, f.minute, u.MBSeconds)
	f.store = state.NewPgStore(pool)
	if replay, err := state.ImportGlobalRuntimeUsage(ctx, f.store, f.trust, e, f.now); err != nil || replay != first {
		t.Fatalf("restart replay: %+v %v", replay, err)
	}
	for _, sql := range []string{`UPDATE global_runtime_usage_minutes SET app_slug='replaced'`, `DELETE FROM global_runtime_usage_minutes`, `TRUNCATE global_runtime_usage_minutes CASCADE`} {
		if _, err := pool.Exec(ctx, sql); err == nil {
			t.Fatalf("audit guard allowed %s", sql)
		}
	}
	parts := runtimeUsageMigrationParts(t)
	if _, err := pool.Exec(ctx, parts[0]); err != nil {
		t.Fatalf("migration replay: %v", err)
	}
	if _, err := pool.Exec(ctx, parts[1]); err == nil || !strings.Contains(err.Error(), "cannot downgrade") {
		t.Fatalf("retained downgrade: %v", err)
	}
	if replay, err := state.ImportGlobalRuntimeUsage(ctx, f.store, f.trust, e, f.now); err != nil || replay != first {
		t.Fatalf("migration changed receipt: %+v %v", replay, err)
	}
}
