// Package state — MemStore alert_presets catalog tests (issue #1233 /
// ADR-123).
//
// The catalog is system-seeded in production by migrations/00353_alert_presets_seed.sql;
// NewMemStore leaves the map empty so a test must seed its own
// rows. The two methods under test (ListAlertPresets,
// AlertPresetByName) are thin O(N) scans; the test pins the
// (category, name) ordering guarantee + the ErrNotFound path so a
// future refactor that swaps the map for a sorted slice (or
// delegates to the pgstore) doesn't drift.
package state

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// seedAlertPresetsCatalog writes a deterministic 4-row catalog into
// the store. The names are out of (category, name) order so the
// ListAlertPresets sort assertion has signal (if the sort step
// regresses, the test fires before any other consumer does).
func seedAlertPresetsCatalog(t *testing.T, m *MemStore) []AlertPreset {
	t.Helper()
	rows := []AlertPreset{
		{
			Name:                   "p95_latency_1s",
			DisplayName:            "p95 exceeds one second",
			Description:            "Fires when rolling p95 latency exceeds 1s.",
			Category:               "reliability",
			Metric:                 "latency_p95_ms",
			Comparison:             "gt",
			Threshold:              1000,
			WindowSpec:             "15m",
			DefaultCooldownMinutes: 15,
			MinimumPlan:            string(api.PlanHobby),
			EnabledInCatalog:       true,
		},
		{
			Name:                   "error_rate_2pct",
			DisplayName:            "Error rate exceeds 2%",
			Description:            "Fires when rolling error rate exceeds 2%.",
			Category:               "reliability",
			Metric:                 "error_rate_pct",
			Comparison:             "gt",
			Threshold:              2,
			WindowSpec:             "15m",
			DefaultCooldownMinutes: 15,
			MinimumPlan:            string(api.PlanHobby),
			EnabledInCatalog:       true,
		},
		{
			Name:                   "cert_expiring_14d",
			DisplayName:            "Domain certificate is expiring",
			Description:            "Fires when a customer's cert has <14d remaining.",
			Category:               "infrastructure",
			Metric:                 "cert_expiry_seconds",
			Comparison:             "lt",
			Threshold:              1209600,
			WindowSpec:             "24h",
			DefaultCooldownMinutes: 1440,
			MinimumPlan:            string(api.PlanHobby),
			EnabledInCatalog:       false,
		},
		{
			Name:                   "api_down",
			DisplayName:            "API is down",
			Description:            "Fires when the customer's last /readyz probe failed.",
			Category:               "availability",
			Metric:                 "api_up",
			Comparison:             "lt",
			Threshold:              1,
			WindowSpec:             "5m",
			DefaultCooldownMinutes: 5,
			MinimumPlan:            string(api.PlanPro),
			EnabledInCatalog:       false,
		},
	}
	for _, p := range rows {
		m.alertPresets[p.Name] = p
	}
	return rows
}

// TestMemStoreAlertPresets_ListOrdered pins the (category, name)
// sort order. The seed inserts "p95_latency_1s" before
// "error_rate_2pct" — both reliability — so the assertion catches
// a sort drift (e.g. an inadvertent swap to `>` on the secondary
// key).
func TestMemStoreAlertPresets_ListOrdered(t *testing.T) {
	m := NewMemStore()
	seedAlertPresetsCatalog(t, m)
	got, err := m.ListAlertPresets(context.Background())
	if err != nil {
		t.Fatalf("ListAlertPresets: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("len = %d; want 4", len(got))
	}
	// Expected order: availability/api_down, infrastructure/cert_expiring_14d,
	// reliability/error_rate_2pct, reliability/p95_latency_1s.
	wantOrder := []string{"api_down", "cert_expiring_14d", "error_rate_2pct", "p95_latency_1s"}
	for i, want := range wantOrder {
		if got[i].Name != want {
			t.Errorf("got[%d].Name = %q; want %q (full order: %v)", i, got[i].Name, want, gotOrder(got))
		}
	}
}

// TestMemStoreAlertPresets_ByName asserts a known row returns
// the right fields and an unknown row returns ErrNotFound.
func TestMemStoreAlertPresets_ByName(t *testing.T) {
	m := NewMemStore()
	seedAlertPresetsCatalog(t, m)
	got, err := m.AlertPresetByName(context.Background(), "cert_expiring_14d")
	if err != nil {
		t.Fatalf("AlertPresetByName: %v", err)
	}
	if got.Metric != "cert_expiry_seconds" {
		t.Errorf("Metric = %q; want cert_expiry_seconds", got.Metric)
	}
	if got.MinimumPlan != "hobby" {
		t.Errorf("MinimumPlan = %q; want hobby", got.MinimumPlan)
	}
	if got.EnabledInCatalog {
		t.Errorf("EnabledInCatalog = true; want false")
	}
	// Unknown name returns ErrNotFound so the handler can
	// translate to 404 (ErrAlertPresetInvalid).
	if _, err := m.AlertPresetByName(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v; want ErrNotFound", err)
	}
}

// TestMemStoreAlertPresets_EmptyCatalog asserts ListAlertPresets
// returns an empty slice (not nil) when the catalog has not been
// seeded. The handler relies on this so it can range over the
// result without a nil check.
func TestMemStoreAlertPresets_EmptyCatalog(t *testing.T) {
	m := NewMemStore()
	got, err := m.ListAlertPresets(context.Background())
	if err != nil {
		t.Fatalf("ListAlertPresets: %v", err)
	}
	if got == nil {
		t.Errorf("got = nil; want empty slice")
	}
	if len(got) != 0 {
		t.Errorf("len = %d; want 0", len(got))
	}
}

// gotOrder is a tiny helper used in error messages so a sort-drift
// failure lists the full ordering at once instead of forcing a
// second pass through -run -v.
func gotOrder(rows []AlertPreset) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Name)
	}
	return out
}
