package state

// adr: 802
import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

func TestPGRegionalRuntimeUsageOutboxContract(t *testing.T) {
	runRegionalUsageOutboxContract(t, func(t *testing.T) regionalUsageFixture {
		s, _, cell, a := releasePGFixture(t)
		return newRegionalUsageFixture(t, s, a, cell)
	})
}

func usageOutboxMigration(t *testing.T) (string, string) {
	t.Helper()
	raw, err := migrations.FS.ReadFile("20261012084500001_regional_runtime_usage_outbox.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, ok := strings.Cut(string(raw), "-- +goose Down")
	if !ok {
		t.Fatal("missing downgrade")
	}
	return up, down
}

func TestPGRegionalRuntimeUsageOutboxGuardsRestartAndRollback(t *testing.T) {
	s, _, cell, a := releasePGFixture(t)
	f := newRegionalUsageFixture(t, s, a, cell)
	ctx := t.Context()
	e := f.envelope(t, f.usage)
	for _, claims := range []string{`{}`, `{"Grant":{},"MinuteMicros":1}`} {
		if _, err := s.pool.Exec(ctx, `INSERT INTO regional_runtime_usage_outbox(app_id,allocation_id,cell_id,minute,usage_claims,envelope) VALUES($1,$2,$3,$4,$5,'{}')`, f.usage.Grant.Binding.RegionalAppID, f.usage.Grant.AllocationID, f.cell.IssuerID(), time.UnixMicro(f.usage.MinuteMicros), claims); err == nil {
			t.Fatal("unbound original issuance inserted", claims)
		}
	}
	first, err := QueueRegionalRuntimeUsage(ctx, s, f.cells, e, f.now)
	if err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{`UPDATE regional_runtime_usage_outbox SET usage_claims='{}' WHERE app_id=$1`, `UPDATE regional_runtime_usage_outbox SET envelope='{}' WHERE app_id=$1`, `UPDATE regional_runtime_usage_outbox SET created_at=clock_timestamp() WHERE app_id=$1`, `DELETE FROM regional_runtime_usage_outbox WHERE app_id=$1`} {
		if _, err := s.pool.Exec(ctx, sql, f.usage.Grant.Binding.RegionalAppID); err == nil {
			t.Fatal("outbox rewrite accepted", sql)
		}
	}
	if _, err := s.pool.Exec(ctx, `TRUNCATE regional_runtime_usage_outbox`); err == nil {
		t.Fatal("retained outbox truncated")
	}
	_, down := usageOutboxMigration(t)
	if _, err := s.pool.Exec(ctx, down); err == nil || !strings.Contains(err.Error(), "cannot discard retained") {
		t.Fatal("populated downgrade", err)
	}
	if row, err := NewPgStore(s.pool).RegionalRuntimeUsageOutbox(ctx, f.usage.Grant.Binding.RegionalAppID, f.usage.Grant.AllocationID, time.UnixMicro(f.usage.MinuteMicros)); err != nil || !reflect.DeepEqual(row, first) {
		t.Fatal("restart lost original outbox", row, err)
	}
	if _, err := s.pool.Exec(ctx, `CREATE FUNCTION fail_usage_ack_test() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected usage ack failure'; END $$; CREATE TRIGGER zz_fail_usage_ack_test AFTER UPDATE ON regional_runtime_usage_outbox FOR EACH ROW EXECUTE FUNCTION fail_usage_ack_test()`); err != nil {
		t.Fatal(err)
	}
	ack := runtimequota.UsageImportAck{Usage: f.usage, ImportedAtMicros: f.now.UnixMicro()}
	signed, err := f.quota.signer.SignUsageImportAck(ack)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AcknowledgeRegionalRuntimeUsage(ctx, s, f.dirs, signed, f.now); err == nil || !strings.Contains(err.Error(), "injected usage ack failure") {
		t.Fatal("failed ack committed", err)
	}
	if rows, err := s.ListPendingRegionalRuntimeUsage(ctx, "", 0, 1); err != nil || len(rows) != 1 || !reflect.DeepEqual(rows[0], first) {
		t.Fatal("ack rollback hid pending item", rows, err)
	}
	if _, err := s.pool.Exec(ctx, `DROP TRIGGER zz_fail_usage_ack_test ON regional_runtime_usage_outbox; DROP FUNCTION fail_usage_ack_test()`); err != nil {
		t.Fatal(err)
	}
	accepted, err := AcknowledgeRegionalRuntimeUsage(ctx, s, f.dirs, signed, f.now)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(ack)
	if _, err := s.pool.Exec(ctx, `UPDATE regional_runtime_usage_outbox SET ack_claims=$1 WHERE app_id=$2`, payload, f.usage.Grant.Binding.RegionalAppID); err == nil {
		t.Fatal("original acknowledgement rewritten")
	}
	if row, err := NewPgStore(s.pool).RegionalRuntimeUsageOutbox(ctx, f.usage.Grant.Binding.RegionalAppID, f.usage.Grant.AllocationID, time.UnixMicro(f.usage.MinuteMicros)); err != nil || !reflect.DeepEqual(row, accepted) {
		t.Fatal(row, err)
	}
}

func TestPGRegionalRuntimeUsageOutboxEmptyMigrationAndJournalLock(t *testing.T) {
	s, _, cell, a := releasePGFixture(t)
	up, down := usageOutboxMigration(t)
	if _, err := s.pool.Exec(t.Context(), down); err != nil {
		t.Fatal("empty downgrade", err)
	}
	if _, err := s.pool.Exec(t.Context(), up); err != nil {
		t.Fatal("upgrade", err)
	}
	f := newRegionalUsageFixture(t, s, a, cell)
	e := f.envelope(t, f.usage)
	claims, err := json.Marshal(f.usage)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := s.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err := tx.Exec(t.Context(), `INSERT INTO regional_runtime_usage_outbox(app_id,allocation_id,cell_id,minute,usage_claims,envelope) VALUES($1,$2,$3,$4,$5,$6)`, f.usage.Grant.Binding.RegionalAppID, f.usage.Grant.AllocationID, f.cell.IssuerID(), time.UnixMicro(f.usage.MinuteMicros), claims, envelope); err != nil {
		t.Fatal(err)
	}
	reader, err := s.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Rollback(t.Context()) }()
	var locked bool
	if err := reader.QueryRow(t.Context(), `SELECT pg_try_advisory_xact_lock(hashtextextended('gregale-regional-runtime-quota:'||$1::uuid::text,0))`, f.usage.Grant.Binding.RegionalAppID).Scan(&locked); err != nil || locked {
		t.Fatal("queue escaped original app journal lock", locked, err)
	}
	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	if rows, err := s.ListPendingRegionalRuntimeUsage(t.Context(), "", 0, 1); err != nil || len(rows) != 0 {
		t.Fatal("rollback retained queue entry", rows, err)
	}
}
