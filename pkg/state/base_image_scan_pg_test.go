//go:build !no_pg

package state

// adr: 435

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPgBaseScanLifecycle(t *testing.T) {
	s, _ := registryVerificationPGStore(t)
	baseScanLifecycle(t, s)
}
func TestPgBaseScanRefusals(t *testing.T) {
	s, _ := registryVerificationPGStore(t)
	baseScanRefusals(t, s)
}

func TestPgBaseScanFreshnessUsesAdvancingStorageClock(t *testing.T) {
	s, pool := registryVerificationPGStore(t)
	in, base := baseScanFixture(t, s)
	in, hash, err := prepareBaseImageScan(in)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	q := sqlc.New()
	if err := q.AuthorizeBaseImageScanInsert(t.Context(), tx, mustPgUUID(in.ID)); err != nil {
		t.Fatal(err)
	}
	// Test-only private SQLC publication with a short, valid lease. The same
	// transaction stays open across expiry to distinguish clock_timestamp()
	// from transaction-frozen now(); this is a storage-clock test, not a scan.
	row, err := q.InsertBaseImageScan(t.Context(), tx, sqlc.InsertBaseImageScanParams{ID: mustPgUUID(in.ID), InputSnapshot: raw, InputHash: hash, ProducerID: mustPgUUID(base.ID), TtlSeconds: 0.3, DbMaxAgeSeconds: api.ApplicationStandardScannerDBMaxAge.Seconds()})
	if err != nil {
		t.Fatal(err)
	}
	if err := q.SelectBaseImageScan(t.Context(), tx, sqlc.SelectBaseImageScanParams{StorageKey: in.Artifact.StorageKey, ID: mustPgUUID(in.ID)}); err != nil {
		t.Fatal(err)
	}
	params := sqlc.GetFreshBaseImageScanParams{ProducerID: mustPgUUID(base.ID), ProducerHash: base.InputHash, DbMaxAgeSeconds: api.ApplicationStandardScannerDBMaxAge.Seconds()}
	if _, err := q.GetFreshBaseImageScan(t.Context(), tx, params); err != nil {
		t.Fatalf("new lease not fresh: %v", err)
	}
	time.Sleep(350 * time.Millisecond)
	if _, err := q.GetFreshBaseImageScan(t.Context(), tx, params); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("transaction clock renewed expired scan: %v", err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetFreshBaseImageScan(t.Context(), base.ID, base.InputHash); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("expired lease stayed usable: %v", err)
	}
	retry, err := s.PublishBaseImageScan(t.Context(), in)
	if err != nil || !retry.ScannedAt.Equal(row.ScannedAt.Time) || !retry.ExpiresAt.Equal(row.ExpiresAt.Time) {
		t.Fatalf("exact retry changed expired lease: %v", err)
	}
}
func TestPgBaseScanAtomicImmutableAndNonwaiting(t *testing.T) {
	s, pool := registryVerificationPGStore(t)
	in, _ := baseScanFixture(t, s)
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), `SELECT pg_advisory_xact_lock(hashtextextended('gregale.base-producer.' || $1::text,0))`, in.Artifact.StorageKey); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err := s.PublishBaseImageScan(ctx, in); !errors.Is(err, ErrApplicationStandardReviewBusy) {
		t.Fatalf("scan waited or bypassed base fence: %v", err)
	}
	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `CREATE FUNCTION reject_base_scan_selection() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected base selection failure'; END; $$; CREATE TRIGGER reject_base_scan_selection BEFORE INSERT ON base_image_scan_current FOR EACH ROW EXECUTE FUNCTION reject_base_scan_selection()`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishBaseImageScan(t.Context(), in); err == nil {
		t.Fatal("injected selection failure accepted")
	} else {
		var pgerr *pgconn.PgError
		if !errors.As(err, &pgerr) || pgerr.Message != "injected base selection failure" {
			t.Fatalf("rollback masked the selection failure: %v", err)
		}
	}
	var records, pointers int
	if err := pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM base_image_scans),(SELECT count(*) FROM base_image_scan_current)`).Scan(&records, &pointers); err != nil || records != 0 || pointers != 0 {
		t.Fatalf("partial base scan: %d records, %d pointers, %v", records, pointers, err)
	}
	if _, err := pool.Exec(t.Context(), `DROP TRIGGER reject_base_scan_selection ON base_image_scan_current; DROP FUNCTION reject_base_scan_selection()`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishBaseImageScan(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{`UPDATE base_image_scans SET expires_at=expires_at WHERE id=$1`, `DELETE FROM base_image_scans WHERE id=$1`, `UPDATE base_image_scan_current SET scan_id=scan_id WHERE scan_id=$1`, `DELETE FROM base_image_scan_current WHERE scan_id=$1`} {
		_, err := pool.Exec(t.Context(), query, in.ID)
		var pgerr *pgconn.PgError
		if !errors.As(err, &pgerr) || pgerr.ConstraintName != "base_image_scan_immutable" {
			t.Fatalf("raw base scan mutation accepted: %v", err)
		}
	}
}
