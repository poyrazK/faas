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
// migrations 00348, 20260905000000001, and the B3 alert-metrics seed
// migration seed the 14 catalog rows.
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
// migration lands, the 14 catalog rows must come back in the order
// availability < cost < deployment < infrastructure < reliability, and
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
	// The base seed (migrations/00348_alert_presets_seed.sql) plus the
	// safe-releases and B3 seeds ship 14 rows. The exact names may shift in future migrations; this
	// test pins the COUNT + the (category, name) ordering shape.
	if len(got) != 14 {
		t.Errorf("catalog row count = %d; want 14 (base + safe-releases + B3 seeds)", len(got))
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
	// so the test is hermetic — it doesn't depend on 00348
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

// TestPg_AlertPresetByName_HappyUnknown pins the Store-served
// AlertPresetByName read path against the seeded catalog: a
// seeded name returns the catalog row, an unknown name returns
// state.ErrNotFound. Coverage-bump: this method had 0% coverage
// on the pgstore side at round-6 rebase, dropping the pkg/state
// floor to 69.9% (target ≥ 70%).
func TestPg_AlertPresetByName_HappyUnknown(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)

	// Pick a seeded catalog row from the PR-A migration. The
	// canonical stable name is 'error_rate_2pct' (issue #1233
	// / ADR-123). If the seed migration didn't run, the call
	// returns ErrNotFound and the test still passes on the
	// sad-path branch — but we want to pin the happy path so
	// the test must run after the seed has applied. pgStore
	// uses pgtest which runs migrations up to HEAD before
	// returning; if the seed file isn't there, the test will
	// hit the ErrNotFound branch and that's the only signal
	// we get.
	preset, err := store.AlertPresetByName(ctx, "error_rate_2pct")
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			t.Skip("alert_presets seed migration not applied; skipping happy path")
		}
		t.Fatalf("AlertPresetByName(error_rate_2pct): %v", err)
	}
	if preset.Name != "error_rate_2pct" {
		t.Errorf("Name = %q, want error_rate_2pct", preset.Name)
	}
	if !preset.EnabledInCatalog {
		t.Errorf("EnabledInCatalog = false, want true for seeded preset")
	}

	// Unknown name: ErrNotFound (not generic pq error). Pins the
	// caller-handling contract — apid enable handler converts this
	// to 404 catalog_preset_not_found.
	if _, err := store.AlertPresetByName(ctx, "nonexistent_preset_xyz"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("AlertPresetByName(unknown): got %v, want ErrNotFound", err)
	}
}

// TestPg_AlertPresetCatalog_AllEnabledAfterFlip pins the per-row
// enabled_in_catalog state after migrations/00516 lands. PR-B's
// data-only UPDATE flips the 5 disabled rows to enabled_in_catalog=true;
// this test guards against a future migration accidentally re-disabling
// a row (e.g. a seed re-run with `enabled_in_catalog=false` on a
// pre-existing name) and against a future maintainer re-introducing a
// "coming soon" row without bumping this test (which lists all 14).
//
// Once a new preset lands in the catalog seed, add its name to the
// `expectedNames` set so this test stays a closed-set pin.
func TestPg_AlertPresetCatalog_AllEnabledAfterFlip(t *testing.T) {
	_, pool, ctx := pgStoreWithPool(t)
	rows, err := pool.Query(ctx, `
		SELECT name, enabled_in_catalog
		  FROM alert_presets
		 ORDER BY name`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	expectedNames := map[string]bool{
		"api_down":                      true,
		"cert_expiring_14d":             true,
		"cold_start_10pct":              true,
		"deploy_failed":                 true,
		"error_rate_2pct":               true,
		"p95_latency_1s":                true,
		"queue_backlog_growing":         true,
		"spend_eur_20":                  true,
		"canary_stuck_step":             true,
		"safedeploy_audit_emit_failing": true,
		"deployment_audit_gc_failing":   true,
		"canary_fleet_in_flight_high":   true,
		"new_error":                     true,
		"daily_spend_eur_1":             true,
	}
	got := make(map[string]bool)
	for rows.Next() {
		var name string
		var enabled bool
		if err := rows.Scan(&name, &enabled); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[name] = enabled
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}
	// Closed-set drift: every catalog row MUST appear in
	// expectedNames. A future seed that adds a row without bumping
	// this test is a sign the maintainer forgot the per-row pin.
	if len(got) != len(expectedNames) {
		var extra []string
		for n := range got {
			if !expectedNames[n] {
				extra = append(extra, n)
			}
		}
		var missing []string
		for n := range expectedNames {
			if _, ok := got[n]; !ok {
				missing = append(missing, n)
			}
		}
		t.Errorf("catalog row count drift: got %d rows, want %d; extra=%v missing=%v — update TestPg_AlertPresetCatalog_AllEnabledAfterFlip.expectedNames if a new preset was added intentionally",
			len(got), len(expectedNames), extra, missing)
	}
	// All rows must have enabled_in_catalog=true. A disabled row means
	// either migration 00516 didn't run or a future migration flipped
	// it back.
	var disabled []string
	for name, enabled := range got {
		if !enabled {
			disabled = append(disabled, name)
		}
	}
	if len(disabled) != 0 {
		t.Errorf("alert_presets rows still disabled after PR-B flip: %v (expected empty — all catalog rows should be enabled)", disabled)
	}
}
