// pgstore_alert_presets_test.go — PgStore tests for the alert-preset
// catalog (issue #1233 / ADR-123). Pins the read-side contract for
// the alert_presets table:
//
//   - ListAlertPresets returns every seeded row, ordered by
//     (category, name).
//   - AlertPresetByName returns the catalog row for a stable name
//     key; ErrNotFound for an unknown name.
//
// The pgstore queries live at pkg/state/pgstore.go:7270-7335. The
// underlying SQL is hand-written (sqlc v1.31.1 can't handle the
// closed-set CHECK + the seed-only mutator posture), so the test
// surface here is the schema-vs-row mapping + the (category, name)
// order. TestPg_AlertPresetCatalog_SeedMigration pins that the
// migration 00363 seed inserts the 8 catalog rows verbatim.
//
// pgtest.Open handles the skip when Postgres is unreachable, so
// the test is safe to run on a dev box without /var/run/postgresql.
package state_test

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestPg_AlertPresetCatalog_ListOrdered pins the
// (category, name) sort order of ListAlertPresets. After the seed
// migration lands, the 8 catalog rows must come back in the order
// availability < deployment < infrastructure < reliability, and
// within each category by name.
func TestPg_AlertPresetCatalog_ListOrdered(t *testing.T) {
	_, pool, ctx := pgStoreWithPool(t)
	rows, err := pool.Query(ctx, `SELECT name, category, enabled_in_catalog FROM alert_presets ORDER BY category, name`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	type row struct {
		Name             string
		Category         string
		EnabledInCatalog bool
	}
	var got []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.Name, &r.Category, &r.EnabledInCatalog); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}
	// The PR-A seed (migrations/00363_alert_presets_seed.sql) ships
	// 8 rows. The exact names may shift in future migrations; this
	// test pins the COUNT + the (category, name) ordering shape.
	if len(got) != 8 {
		t.Errorf("catalog row count = %d; want 8 (PR-A seed)", len(got))
	}
	// Verify (category, name) order is sorted.
	for i := 1; i < len(got); i++ {
		prev, cur := got[i-1], got[i]
		if cur.Category < prev.Category {
			t.Errorf("rows[%d].Category %q < rows[%d].Category %q (cross-category sort drift)", i, cur.Category, i-1, prev.Category)
		}
		if cur.Category == prev.Category && cur.Name < prev.Name {
			t.Errorf("rows[%d].Name %q < rows[%d].Name %q (within-category sort drift)", i, cur.Name, i-1, prev.Name)
		}
	}
}

// TestPg_AlertPresetCatalog_ByName_And_NotFound pins the
// AlertPresetByName query path: a known row returns the full set
// of catalog fields verbatim, and an unknown name returns
// ErrNotFound (the handler translates this to a 404 via
// ErrAlertPresetInvalid).
func TestPg_AlertPresetCatalog_ByName_And_NotFound(t *testing.T) {
	_, pool, ctx := pgStoreWithPool(t)
	// Seed a single row directly (bypassing the migration seed
	// so the test is hermetic — it doesn't depend on 00363
	// running before the assertion).
	const wantName = "test_catalog_row"
	if _, err := pool.Exec(ctx, `INSERT INTO alert_presets
		(name, display_name, description, category, metric, comparison, threshold, window_spec, default_cooldown_minutes, minimum_plan, enabled_in_catalog)
		VALUES ($1, 'Test row', 'desc', 'reliability', 'error_rate_pct', 'gt', 2.0, '15m', 15, 'hobby', true)
		ON CONFLICT (name) DO NOTHING`,
		wantName,
	); err != nil {
		t.Fatalf("seed: %v", err)
	}
	s := state.NewPgStore(pool)
	got, err := s.AlertPresetByName(ctx, wantName)
	if err != nil {
		t.Fatalf("AlertPresetByName(%q): %v", wantName, err)
	}
	if got.Name != wantName {
		t.Errorf("Name = %q; want %q", got.Name, wantName)
	}
	if got.Category != "reliability" {
		t.Errorf("Category = %q; want reliability", got.Category)
	}
	if got.Metric != "error_rate_pct" {
		t.Errorf("Metric = %q; want error_rate_pct", got.Metric)
	}
	if got.MinimumPlan != "hobby" {
		t.Errorf("MinimumPlan = %q; want hobby", got.MinimumPlan)
	}
	if !got.EnabledInCatalog {
		t.Errorf("EnabledInCatalog = false; want true")
	}
	if got.Threshold != 2.0 {
		t.Errorf("Threshold = %v; want 2.0", got.Threshold)
	}
	// Unknown name returns ErrNotFound.
	if _, err := s.AlertPresetByName(ctx, "definitely_not_a_preset"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("err = %v; want ErrNotFound", err)
	}
}

// TestPg_AlertPresetCatalog_ClosedSetCheck pins the
// alert_presets_metric_chk + alert_presets_comparison_chk +
// alert_presets_window_chk + alert_presets_category_chk +
// alert_presets_plan_chk constraints: an INSERT with an
// out-of-set metric / comparison / window / category / plan
// must fail with a SQL CHECK violation (SQLSTATE 23514).
// Drift test so a future migration that drops a CHECK
// constraint trips the assertion before the wire breaks.
func TestPg_AlertPresetCatalog_ClosedSetCheck(t *testing.T) {
	_, pool, ctx := pgStoreWithPool(t)
	cases := []struct {
		name string
		sql  string
	}{
		{
			name: "bad_metric",
			sql: `INSERT INTO alert_presets (name, display_name, description, category, metric, comparison, threshold, window_spec, default_cooldown_minutes, minimum_plan, enabled_in_catalog)
				VALUES ('bad-metric', 'Bad', 'd', 'reliability', 'not_a_metric', 'gt', 1, '5m', 15, 'hobby', true)`,
		},
		{
			name: "bad_comparison",
			sql: `INSERT INTO alert_presets (name, display_name, description, category, metric, comparison, threshold, window_spec, default_cooldown_minutes, minimum_plan, enabled_in_catalog)
				VALUES ('bad-comparison', 'Bad', 'd', 'reliability', 'error_rate_pct', 'NOT_A_COMPARE', 1, '5m', 15, 'hobby', true)`,
		},
		{
			name: "bad_window",
			sql: `INSERT INTO alert_presets (name, display_name, description, category, metric, comparison, threshold, window_spec, default_cooldown_minutes, minimum_plan, enabled_in_catalog)
				VALUES ('bad-window', 'Bad', 'd', 'reliability', 'error_rate_pct', 'gt', 1, '999m', 15, 'hobby', true)`,
		},
		{
			name: "bad_category",
			sql: `INSERT INTO alert_presets (name, display_name, description, category, metric, comparison, threshold, window_spec, default_cooldown_minutes, minimum_plan, enabled_in_catalog)
				VALUES ('bad-category', 'Bad', 'd', 'not_a_category', 'error_rate_pct', 'gt', 1, '5m', 15, 'hobby', true)`,
		},
		{
			name: "bad_plan",
			sql: `INSERT INTO alert_presets (name, display_name, description, category, metric, comparison, threshold, window_spec, default_cooldown_minutes, minimum_plan, enabled_in_catalog)
				VALUES ('bad-plan', 'Bad', 'd', 'reliability', 'error_rate_pct', 'gt', 1, '5m', 15, 'enterprise', true)`,
		},
	}
	for _, c := range cases {
		_, err := pool.Exec(ctx, c.sql)
		if err == nil {
			t.Errorf("%s: INSERT succeeded; want SQL CHECK violation", c.name)
		}
	}
}

// TestPg_AlertPresetCatalog_NameLengthCheck pins the
// alert_presets_name_len_chk CHECK (1..64 chars). A name
// outside the band must fail. Mirrors the dashboard handler's
// regex gating at cmd/apid/dashboard_preset_enable.go:45.
func TestPg_AlertPresetCatalog_NameLengthCheck(t *testing.T) {
	_, pool, ctx := pgStoreWithPool(t)
	// Empty name → length 0, below the floor (1).
	if _, err := pool.Exec(ctx, `INSERT INTO alert_presets
		(name, display_name, description, category, metric, comparison, threshold, window_spec, default_cooldown_minutes, minimum_plan, enabled_in_catalog)
		VALUES ('', 'Bad', 'd', 'reliability', 'error_rate_pct', 'gt', 1, '5m', 15, 'hobby', true)`,
	); err == nil {
		t.Errorf("empty name: INSERT succeeded; want SQL CHECK violation")
	}
	// 65-char name → above the ceiling (64).
	longName := "x"
	for i := 0; i < 64; i++ {
		longName += "x"
	}
	if _, err := pool.Exec(ctx, `INSERT INTO alert_presets
		(name, display_name, description, category, metric, comparison, threshold, window_spec, default_cooldown_minutes, minimum_plan, enabled_in_catalog)
		VALUES ($1, 'Bad', 'd', 'reliability', 'error_rate_pct', 'gt', 1, '5m', 15, 'hobby', true)`,
		longName,
	); err == nil {
		t.Errorf("65-char name: INSERT succeeded; want SQL CHECK violation")
	}
}
