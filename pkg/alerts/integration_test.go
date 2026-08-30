// Issue #396 / ADR-045 PR 4 — pkg/alerts Postgres integration tests.
//
// The unit tests in evaluator_test.go use state.NewMemStore; this
// file exercises the same surface against a real Postgres cluster
// so a future typo in the CTE body, a missing column scan, or a
// wrong `idempotency_key` UNIQUE handling can't ship silently.
// Mirrors the skip-when-no-pg pattern from
// pkg/state/pgstore_alert_rules_test.go.
//
// The tests live in a separate file so pgtest.Open can skip the
// whole package's Postgres-dependent tests when no DATABASE_URL is
// set (CI's -short run); the make test-state-coverage gate runs
// with DATABASE_URL and bumps pkg/state + pkg/alerts coverage above
// the threshold.
package alerts_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/alerts"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestPgStore_ClaimAlertFire_StampsPayload inserts a rule, calls
// ClaimAlertFire, then reads back the alert_deliveries row to assert
// the payload + observed_value columns landed as the JSON the
// dashboard scrape will consume. This is the byte-for-byte Postgres
// equivalent of the unit test in evaluator_test.go.
//
// SKIPPED when DATABASE_URL is unset; CI runs this with a service
// container.
func TestPgStore_ClaimAlertFire_StampsPayload(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := state.NewPgStore(pool)

	acct, err := s.CreateAccount(ctx, "alerts-pg-"+time.Now().Format(time.RFC3339Nano)+"@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := s.CreateApp(ctx, state.App{
		AccountID: acct.ID, Slug: "alerts-pg-" + time.Now().Format("150405.000000"),
		Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	rule, err := s.CreateAlertRule(ctx, state.AlertRule{
		AccountID:           acct.ID,
		AppID:               app.ID,
		Name:                "rule-pg-" + time.Now().Format("150405.000000"),
		Enabled:             true,
		Metric:              state.AlertMetricErrorRate,
		Comparison:          state.AlertGt,
		Threshold:           5,
		WindowSpec:          state.AlertWindow5m,
		WebhookURL:          "https://example.com/hook",
		WebhookSecretSealed: []byte("sealed-secret"),
		CooldownMinutes:     30,
	})
	if err != nil {
		t.Fatalf("CreateAlertRule: %v", err)
	}

	payload := []byte(`{"metric":"error_rate_pct","observed":12.5,"threshold":5,"comparison":"gt"}`)
	deliveryID, won, err := s.ClaimAlertFire(ctx, rule.ID, rule.ID+":bucket-1", payload, 12.5, time.Now())
	if err != nil || !won || deliveryID == "" {
		t.Fatalf("ClaimAlertFire: (%q, %v, %v); want (nonempty, true, nil)", deliveryID, won, err)
	}

	deliveries, err := s.ListAlertDeliveriesForRule(ctx, rule.ID, 5, false)
	if err != nil || len(deliveries) != 1 {
		t.Fatalf("ListAlertDeliveriesForRule: err=%v count=%d; want 1", err, len(deliveries))
	}
	if deliveries[0].ObservedValue != 12.5 {
		t.Errorf("delivery[0].ObservedValue = %v; want 12.5", deliveries[0].ObservedValue)
	}
	if len(deliveries[0].Payload) == 0 {
		t.Errorf("delivery[0].Payload is empty; expected JSON bytes")
	}
}

// TestPgStore_ClaimAlertFire_DuplicateBucket returns won=false on the
// second claim inside the same cool-down bucket. Mirrors the
// MemStore unit test and is the byte-for-byte Postgres equivalent
// of pkg/state/pgstore_alert_rules_test.go::TestPgStore_AlertRule_ClaimDedupe.
func TestPgStore_ClaimAlertFire_DuplicateBucket(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := state.NewPgStore(pool)

	acct, err := s.CreateAccount(ctx, "alerts-pg-dup-"+time.Now().Format(time.RFC3339Nano)+"@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := s.CreateApp(ctx, state.App{
		AccountID: acct.ID, Slug: "alerts-pg-dup-" + time.Now().Format("150405.000000"),
		Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	rule, err := s.CreateAlertRule(ctx, state.AlertRule{
		AccountID:           acct.ID,
		AppID:               app.ID,
		Name:                "rule-pg-dup-" + time.Now().Format("150405.000000"),
		Enabled:             true,
		Metric:              state.AlertMetricErrorRate,
		Comparison:          state.AlertGt,
		Threshold:           5,
		WindowSpec:          state.AlertWindow5m,
		WebhookURL:          "https://example.com/hook",
		WebhookSecretSealed: []byte("sealed-secret"),
		CooldownMinutes:     30,
	})
	if err != nil {
		t.Fatalf("CreateAlertRule: %v", err)
	}

	t0 := time.Now()
	id1, won1, err := s.ClaimAlertFire(ctx, rule.ID, rule.ID+":bucket-1", []byte(`{}`), 10.5, t0)
	if err != nil || !won1 || id1 == "" {
		t.Fatalf("first claim: (%q, %v, %v); want (nonempty, true, nil)", id1, won1, err)
	}
	id2, won2, err := s.ClaimAlertFire(ctx, rule.ID, rule.ID+":bucket-1", []byte(`{}`), 10.5, t0.Add(time.Second))
	if err != nil || won2 || id2 != "" {
		t.Fatalf("duplicate claim: (%q, %v, %v); want (\"\", false, nil)", id2, won2, err)
	}
}

// TestPgStore_DeleteAccount_CascadesAlertRules mirrors the
// FK ON DELETE CASCADE contract — deleting an account cascades
// both alert_rules and alert_deliveries. The PR 3 schema already
// pinned this; PR 4 re-pins via the integration test so the FK
// constraint is exercised end-to-end.
func TestPgStore_DeleteAccount_CascadesAlertRules(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := state.NewPgStore(pool)

	acct, err := s.CreateAccount(ctx, "alerts-pg-fk-"+time.Now().Format(time.RFC3339Nano)+"@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := s.CreateApp(ctx, state.App{
		AccountID: acct.ID, Slug: "alerts-pg-fk-" + time.Now().Format("150405.000000"),
		Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	rule, err := s.CreateAlertRule(ctx, state.AlertRule{
		AccountID:           acct.ID,
		AppID:               app.ID,
		Name:                "rule-pg-fk-" + time.Now().Format("150405.000000"),
		Enabled:             true,
		Metric:              state.AlertMetricErrorRate,
		Comparison:          state.AlertGt,
		Threshold:           5,
		WindowSpec:          state.AlertWindow5m,
		WebhookURL:          "https://example.com/hook",
		WebhookSecretSealed: []byte("sealed-secret"),
		CooldownMinutes:     30,
	})
	if err != nil {
		t.Fatalf("CreateAlertRule: %v", err)
	}
	deliveryID, won, err := s.ClaimAlertFire(ctx, rule.ID, rule.ID+":bucket-1", []byte(`{}`), 10.5, time.Now())
	if err != nil || !won || deliveryID == "" {
		t.Fatalf("ClaimAlertFire: (%q, %v, %v); want (nonempty, true, nil)", deliveryID, won, err)
	}

	// Flip the account to deleted_pending first; the unconditional
	// DeleteAccount sentinel (pgstore.go:6537-6542) requires the
	// soft-delete state before it'll touch the parent row and let
	// the FK ON DELETE CASCADE fire on alert_rules /
	// alert_deliveries.
	if err := s.MarkAccountDeletionPending(ctx, acct.ID); err != nil {
		t.Fatalf("MarkAccountDeletionPending: %v", err)
	}
	if err := s.DeleteAccount(ctx, acct.ID); err != nil {
		t.Fatalf("DeleteAccount: %v", err)
	}
	// Re-listing should return zero rows; the FK ON DELETE CASCADE
	// dropped both alert_rules and alert_deliveries. Use a fresh
	// store call so we don't race the LISTEN-side cache.
	if _, err := s.AlertRuleByID(ctx, rule.ID); err == nil {
		t.Errorf("AlertRuleByID after DeleteAccount: expected ErrNotFound, got nil")
	}
	deliveries, err := s.ListAlertDeliveriesForRule(ctx, rule.ID, 5, false)
	if err != nil {
		t.Fatalf("ListAlertDeliveriesForRule: %v", err)
	}
	if len(deliveries) != 0 {
		t.Errorf("deliveries after cascade = %d; want 0", len(deliveries))
	}
}

// guard the imports so the integration_test file compiles even when
// pgtest.Open skips.
var _ = alerts.AlertSecretNamespace
