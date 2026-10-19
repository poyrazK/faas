package dataapiusage_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state/dataapiusage"
)

const (
	account = "11111111-1111-1111-1111-111111111111"
	other   = "22222222-2222-2222-2222-222222222222"
	app     = "33333333-3333-3333-3333-333333333333"
	gone    = "44444444-4444-4444-4444-444444444444"
)

// open applies the usage migration over a minimal apps table, so the store
// runs against the real constraints without the full schema.
func open(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := pgtest.Open(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `CREATE TABLE apps (id uuid PRIMARY KEY, account_id uuid NOT NULL, deleted_at timestamptz)`); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"20261019131100664_data_api_usage.sql", "20261019172254731_data_api_access_configs.sql"} {
		raw, err := migrations.FS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		up, _, _ := strings.Cut(string(raw), "-- +goose Down")
		if _, err := pool.Exec(ctx, up); err != nil {
			t.Fatalf("apply migration %s: %v", name, err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO apps (id, account_id) VALUES ($1, $2), ($3, $2)`, app, account, gone); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE apps SET deleted_at = now() WHERE id = $1`, gone); err != nil {
		t.Fatal(err)
	}
	return pool
}

func TestRecordAddsEachReportOnceAndReturnsMonthToDate(t *testing.T) {
	store := dataapiusage.New(open(t))
	ctx := context.Background()
	now := time.Date(2026, 10, 19, 10, 30, 0, 0, time.UTC)
	hour := now.Truncate(time.Hour)
	delta := []dataapiusage.Hour{
		{Hour: hour.Add(-time.Hour), Counts: dataapiusage.Counts{UserRequests: 3, AnonymousRequests: 2, ServerRequests: 1, RowsReturned: 40, ResponseBytes: 900}},
		{Hour: hour, Counts: dataapiusage.Counts{AnonymousRequests: 5, RejectedRequests: 2, ErrorRequests: 1, CappedRequests: 4}},
	}
	mtd, err := store.Record(ctx, account, app, "i-1:1", delta, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if mtd.Requests != 11 || mtd.AnonymousRequests != 7 || !mtd.Month.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("month to date = %+v", mtd)
	}
	again, err := store.Record(ctx, account, app, "i-1:1", delta, nil, now)
	if err != nil || again != mtd {
		t.Fatalf("a retried report must add nothing: %+v (%v)", again, err)
	}
	if _, err = store.Record(ctx, account, app, "i-1:2", delta[1:], nil, now); err != nil {
		t.Fatal(err)
	}
	hours, err := store.Hours(ctx, app, hour.Add(-24*time.Hour), hour.Add(time.Hour))
	if err != nil || len(hours) != 2 {
		t.Fatalf("hours = %+v (%v)", hours, err)
	}
	if hours[1].AnonymousRequests != 10 || hours[1].CappedRequests != 8 || hours[0].RowsReturned != 40 || !hours[0].Hour.Equal(hour.Add(-time.Hour)) {
		t.Fatalf("hours were not summed: %+v", hours)
	}
}

func TestRecordRefusesForeignAndDeletedApps(t *testing.T) {
	store := dataapiusage.New(open(t))
	ctx := context.Background()
	now := time.Now()
	for _, tc := range []struct{ account, app string }{{other, app}, {account, gone}, {account, "55555555-5555-5555-5555-555555555555"}} {
		if _, err := store.Record(ctx, tc.account, tc.app, "r", nil, nil, now); !errors.Is(err, dataapiusage.ErrNotFound) {
			t.Fatalf("%+v: err = %v", tc, err)
		}
	}
}

func TestMonthToDateCountsOnlyTheCurrentUTCMonth(t *testing.T) {
	store := dataapiusage.New(open(t))
	ctx := context.Background()
	now := time.Date(2026, 11, 1, 0, 30, 0, 0, time.UTC)
	delta := []dataapiusage.Hour{
		{Hour: time.Date(2026, 10, 31, 23, 0, 0, 0, time.UTC), Counts: dataapiusage.Counts{UserRequests: 100}},
		{Hour: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), Counts: dataapiusage.Counts{UserRequests: 1, AnonymousRequests: 1}},
	}
	mtd, err := store.Record(ctx, account, app, "r1", delta, nil, now)
	if err != nil || mtd.Requests != 2 || mtd.AnonymousRequests != 1 {
		t.Fatalf("mtd = %+v (%v)", mtd, err)
	}
	if read, err := store.MonthToDate(ctx, app, now); err != nil || read != mtd {
		t.Fatalf("read = %+v (%v)", read, err)
	}
}

func TestRecordPrunesExpiredReportsAndHours(t *testing.T) {
	pool := open(t)
	store := dataapiusage.New(pool)
	ctx := context.Background()
	old := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	if _, err := store.Record(ctx, account, app, "old", []dataapiusage.Hour{{Hour: old, Counts: dataapiusage.Counts{UserRequests: 1}}}, nil, old); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE data_api_usage_reports SET received_at = $1`, old); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	if _, err := store.Record(ctx, account, app, "new", nil, nil, now); err != nil {
		t.Fatal(err)
	}
	var reports, hours int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM data_api_usage_reports), (SELECT count(*) FROM data_api_usage_hourly)`).Scan(&reports, &hours); err != nil {
		t.Fatal(err)
	}
	if reports != 1 || hours != 0 {
		t.Fatalf("reports = %d hours = %d after pruning", reports, hours)
	}
}

func TestTheSchemaRefusesUnalignedHoursAndMalformedReportIDs(t *testing.T) {
	pool := open(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO data_api_usage_hourly (app_id, hour) VALUES ($1, '2026-10-19 10:30:00+00')`, app); err == nil {
		t.Fatal("an unaligned hour was stored")
	}
	if _, err := pool.Exec(ctx, `INSERT INTO data_api_usage_hourly (app_id, hour) VALUES ($1, '2026-10-19 10:00:00+05:30')`, app); err == nil {
		t.Fatal("an hour aligned only in a half-hour zone was stored")
	}
	if _, err := pool.Exec(ctx, `INSERT INTO data_api_usage_reports (app_id, report_id) VALUES ($1, 'bad id')`, app); err == nil {
		t.Fatal("a malformed report id was stored")
	}
}

func TestAccessSettingsAreReplacedReadAndForgotten(t *testing.T) {
	store := dataapiusage.New(open(t))
	ctx := context.Background()
	now := time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)
	if _, _, ok, err := store.Access(ctx, app); ok || err != nil {
		t.Fatalf("an app that never reported has settings: %v %v", ok, err)
	}
	first := []byte(`{"version":1,"schemas":["api"],"public_read":["api.products"]}`)
	if _, err := store.Record(ctx, account, app, "r1", nil, first, now); err != nil {
		t.Fatal(err)
	}
	second := []byte(`{"version":1,"schemas":["api"]}`)
	if _, err := store.Record(ctx, account, app, "r2", nil, second, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Record(ctx, account, app, "r3", nil, nil, now); err != nil {
		t.Fatal(err)
	}
	config, reported, ok, err := store.Access(ctx, app)
	if err != nil || !ok || reported.IsZero() || !strings.Contains(string(config), `"schemas"`) || strings.Contains(string(config), "public_read") {
		t.Fatalf("settings = %s %v %v %v (the latest report replaces; a report without settings keeps them)", config, reported, ok, err)
	}
	if err := store.DeleteAccess(ctx, other, app); err != nil {
		t.Fatal(err)
	}
	if _, _, ok, _ := store.Access(ctx, app); !ok {
		t.Fatal("another account cleared the settings")
	}
	if err := store.DeleteAccess(ctx, account, app); err != nil {
		t.Fatal(err)
	}
	if _, _, ok, _ := store.Access(ctx, app); ok {
		t.Fatal("settings survived deletion")
	}
}
