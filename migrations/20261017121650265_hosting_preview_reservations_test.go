//go:build !no_pg

package migrations_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestHostingPreviewReservationsMigrationAndExpiry(t *testing.T) {
	// adr: 202610171216 — legacy intents stay unreserved; expiry frees only unused holds.
	pool := pgtest.Open(t)
	ctx := t.Context()
	const site = "11111111-1111-4111-8111-111111111111"
	raw, err := migrations.FS.ReadFile("20261017121650265_hosting_preview_reservations.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	_, err = pool.Exec(ctx, `CREATE TABLE hosting_sites(id uuid PRIMARY KEY,account_id uuid);
 CREATE TABLE hosting_releases(id uuid PRIMARY KEY,site_id uuid,state text);
 CREATE TABLE hosting_previews(id uuid PRIMARY KEY,site_id uuid);
 CREATE TABLE hosting_ci_runs(id uuid PRIMARY KEY,site_id uuid,release_id uuid,canceled boolean DEFAULT false,preview_created boolean DEFAULT false,preview_expires_at timestamptz);
 INSERT INTO hosting_sites VALUES('11111111-1111-4111-8111-111111111111','11111111-1111-4111-8111-111111111111');
 INSERT INTO hosting_releases SELECT id,id,'pending' FROM hosting_sites;
 INSERT INTO hosting_ci_runs(id,site_id,release_id,preview_expires_at) SELECT id,id,id,clock_timestamp()+interval '1 hour' FROM hosting_sites;`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, parts[0]); err != nil {
		t.Fatal(err)
	}
	var reserved bool
	if err = pool.QueryRow(ctx, `SELECT preview_reserved FROM hosting_ci_runs`).Scan(&reserved); err != nil || reserved {
		t.Fatal("backfill", reserved, err)
	}
	si := pgtype.UUID{Bytes: uuid.MustParse(site), Valid: true}
	zero := pgtype.UUID{Bytes: uuid.Nil, Valid: true}
	now := time.Now().UTC()
	capacity := func(at time.Time) sqlc.HostingPreviewCapacityRow {
		t.Helper()
		out, e := sqlc.New().HostingPreviewCapacity(ctx, pool, sqlc.HostingPreviewCapacityParams{AccountID: si, SiteID: si, ExcludeRunID: zero, ObservedAt: pgtype.Timestamptz{Time: at, Valid: true}})
		if e != nil {
			t.Fatal(e)
		}
		return out
	}
	if c := capacity(now); c.Reservations != 0 {
		t.Fatal(c)
	}
	if _, err = pool.Exec(ctx, `UPDATE hosting_ci_runs SET preview_reserved=true`); err != nil {
		t.Fatal(err)
	}
	if c := capacity(now); c.Reservations != 1 {
		t.Fatal(c)
	}
	if c := capacity(now.Add(2 * time.Hour)); c.Reservations != 0 {
		t.Fatal("expired hold", c)
	}
	if _, err = pool.Exec(ctx, `UPDATE hosting_releases SET state='retiring'`); err != nil {
		t.Fatal(err)
	}
	if c := capacity(now); c.Reservations != 0 {
		t.Fatal("retired hold", c)
	}
	if _, err = pool.Exec(ctx, `UPDATE hosting_releases SET state='ready'; UPDATE hosting_ci_runs SET preview_created=true; INSERT INTO hosting_previews SELECT id,id FROM hosting_sites;`); err != nil {
		t.Fatal(err)
	}
	if c := capacity(now.Add(2 * time.Hour)); c.Reservations != 0 || c.Previews != 1 {
		t.Fatal("converted/expired preview", c)
	}
	if _, err = pool.Exec(ctx, parts[1]); err == nil {
		t.Fatal("downgrade erased reservation identity")
	}
	if _, err = pool.Exec(ctx, `DELETE FROM hosting_ci_runs`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, parts[1]); err != nil {
		t.Fatal("drained downgrade", err)
	}
}
