//go:build !no_pg

// adr: 20261017210000
package migrations_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestRegressionReviewVersionFastDefaultAndLegacyHistory(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	raw, err := migrations.FS.ReadFile("20261017210000001_regression_review_version.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, _, ok := strings.Cut(string(raw), "-- +goose Down")
	if !ok {
		t.Fatal("missing Down migration")
	}
	down := strings.SplitN(string(raw), "-- +goose Down", 2)[1]
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	schema := "triage_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := tx.Exec(t.Context(), "CREATE SCHEMA "+schema+"; SET LOCAL search_path="+schema+", public; CREATE TABLE debug_regression_observations(id integer PRIMARY KEY,payload integer DEFAULT 0); INSERT INTO debug_regression_observations(id) VALUES(1),(2)"); err != nil {
		t.Fatal(err)
	}
	var before, after uint32
	if err := tx.QueryRow(t.Context(), "SELECT relfilenode FROM pg_class WHERE oid='debug_regression_observations'::regclass").Scan(&before); err != nil {
		t.Fatal(err)
	}
	check := func() {
		t.Helper()
		if _, err := tx.Exec(t.Context(), up); err != nil {
			t.Fatal(err)
		}
		if err := tx.QueryRow(t.Context(), "SELECT relfilenode FROM pg_class WHERE oid='debug_regression_observations'::regclass").Scan(&after); err != nil {
			t.Fatal(err)
		}
		if before != after {
			t.Fatal("review migration rewrote existing history")
		}
		var legacy1, legacy2, inserted, updated, recreated string
		if err := tx.QueryRow(t.Context(), "SELECT review_version FROM debug_regression_observations WHERE id=1").Scan(&legacy1); err != nil {
			t.Fatal(err)
		}
		if err := tx.QueryRow(t.Context(), "SELECT review_version FROM debug_regression_observations WHERE id=2").Scan(&legacy2); err != nil {
			t.Fatal(err)
		}
		if legacy1 != "00000000-0000-0000-0000-000000000001" || legacy2 != legacy1 {
			t.Fatal("legacy fast default missing")
		}
		if err := tx.QueryRow(t.Context(), "INSERT INTO debug_regression_observations(id) VALUES(3) RETURNING review_version").Scan(&inserted); err != nil {
			t.Fatal(err)
		}
		if err := tx.QueryRow(t.Context(), "UPDATE debug_regression_observations SET payload=payload WHERE id=1 RETURNING review_version").Scan(&updated); err != nil {
			t.Fatal(err)
		}
		if inserted == legacy1 || updated == legacy1 || inserted == updated {
			t.Fatal("insert/update failed to rotate version")
		}
		if _, err := tx.Exec(t.Context(), "DELETE FROM debug_regression_observations WHERE id=3"); err != nil {
			t.Fatal(err)
		}
		if err := tx.QueryRow(t.Context(), "INSERT INTO debug_regression_observations(id) VALUES(3) RETURNING review_version").Scan(&recreated); err != nil {
			t.Fatal(err)
		}
		if recreated == inserted {
			t.Fatal("recreated lifecycle reused version")
		}
		// A repeated Up must not rotate stored versions or replace a later fence.
		if _, err := tx.Exec(t.Context(), `
ALTER TABLE debug_regression_observations DROP CONSTRAINT debug_regression_review_version_nonzero;
ALTER TABLE debug_regression_observations ADD CONSTRAINT debug_regression_review_version_nonzero
    CHECK (review_version <> '00000000-0000-0000-0000-000000000000'::uuid AND id > 0);
CREATE OR REPLACE FUNCTION advance_regression_review_version() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.id <= 0 THEN RAISE EXCEPTION 'later review fence'; END IF;
    NEW.review_version := gen_random_uuid();
    RETURN NEW;
END;
$$;
ALTER TABLE debug_regression_observations ENABLE ALWAYS TRIGGER regression_review_version;
`); err != nil {
			t.Fatal(err)
		}
		fingerprint := func() string {
			t.Helper()
			var value string
			if err := tx.QueryRow(t.Context(), `SELECT
    (SELECT string_agg(id::text || ':' || review_version::text, ',' ORDER BY id) FROM debug_regression_observations) ||
    pg_get_functiondef('advance_regression_review_version()'::regprocedure) ||
    (SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid='debug_regression_observations'::regclass AND conname='debug_regression_review_version_nonzero') ||
    (SELECT tgenabled::text || pg_get_triggerdef(oid) FROM pg_trigger WHERE tgrelid='debug_regression_observations'::regclass AND tgname='regression_review_version')`).Scan(&value); err != nil {
				t.Fatal(err)
			}
			return value
		}
		original := fingerprint()
		if _, err := tx.Exec(t.Context(), up); err != nil {
			t.Fatal(err)
		}
		if fingerprint() != original {
			t.Fatal("migration replay changed stored versions or replaced a later fence")
		}
	}
	check()
	if _, err := tx.Exec(t.Context(), down); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), "DELETE FROM debug_regression_observations WHERE id=3"); err != nil {
		t.Fatal(err)
	}
	check()
}
