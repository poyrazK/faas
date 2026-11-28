//go:build !no_pg

package state

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// adr: 20261104120000 — Postgres copies production's active consumers, keys
// (with their secrets) and plan assignments, and its alert rules paused;
// publication proves them; and revoking production's key or consumer
// revokes the stage's copy, whatever path revokes it (amendment 2026-11-28).
func TestPgCloneCopiesConsumersAndPausedAlertRules(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	s := NewPgStore(pool)
	suffix := uuid.NewString()[:8]
	account, err := s.CreateAccount(ctx, "clone-identities-"+suffix+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.CreateProject(ctx, Project{AccountID: account.ID, Slug: "clone-identities-" + suffix})
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, App{AccountID: account.ID, ProjectID: project.ID, Slug: "clone-identities-" + suffix,
		Type: AppTypeApp, Status: AppActive, RAMMB: 128, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	stage, err := s.CreateProjectEnvironment(ctx, ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := s.CreateAPIConsumer(ctx, account.ID, app.ID, "cust-1", "Acme")
	if err != nil {
		t.Fatal(err)
	}
	_, prefix, hash, err := api.GenerateConsumerKey()
	if err != nil {
		t.Fatal(err)
	}
	key, err := s.CreateConsumerKeyForConsumer(ctx, account.ID, consumer.ID, "primary", prefix, hash, []string{"read"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := s.CreateAPIConsumerPlan(ctx, APIConsumerPlan{AccountID: account.ID, AppID: app.ID, Name: "free"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AssignAPIConsumerPlan(ctx, APIConsumerPlanAssignment{AccountID: account.ID, AppID: app.ID, ConsumerID: consumer.ID,
		PlanID: plan.ID, EffectiveFrom: time.Now().Add(-time.Hour).UTC().Truncate(time.Microsecond)}); err != nil {
		t.Fatal(err)
	}
	rule, err := s.CreateAlertRule(ctx, AlertRule{AccountID: account.ID, AppID: app.ID, Name: "errors", Enabled: true,
		Metric: AlertMetricErrorRate, Comparison: AlertGt, Threshold: 5, WindowSpec: AlertWindow5m,
		WebhookURL: "https://example.test/hook", WebhookSecretSealed: []byte("sealed"), CooldownMinutes: 30})
	if err != nil {
		t.Fatal(err)
	}

	production := pgtype.UUID{}
	consumers, err := captureCloneConsumersDB(ctx, pool, app.ID, production)
	if err != nil || len(consumers.Consumers) != 1 || len(consumers.Consumers[0].Keys) != 1 || len(consumers.Consumers[0].Plans) != 1 {
		t.Fatalf("captured consumers = %+v, %v", consumers, err)
	}
	rules, err := captureCloneAlertRulesDB(ctx, pool, app.ID, production)
	if err != nil || len(rules.Rules) != 1 || !rules.Rules[0].Rule.Enabled {
		t.Fatalf("captured rules = %+v, %v", rules, err)
	}
	if err := requireCloneIdentitiesCapturedDB(ctx, pool, project.ID, production, []string{app.ID}); err != nil {
		t.Fatalf("captured app: %v", err)
	}
	if err := requireCloneIdentitiesCapturedDB(ctx, pool, project.ID, production, nil); !errors.Is(err, ErrProjectEnvironmentCloneConsumerCaptureUnavailable) {
		t.Fatalf("uncaptured app = %v", err)
	}
	record := projectCloneWorkloadRecord{snapshot: projectCloneWorkloadSnapshot{Consumers: &consumers, AlertRules: &rules}}
	record.AppID, record.WorkloadSlug = app.ID, "web"
	records := []projectCloneWorkloadRecord{record}
	clone := ProjectEnvironmentClone{AccountID: account.ID, ProjectID: project.ID,
		capturedConsumers: capturedCloneConsumers(records), capturedAlertRules: capturedCloneAlertRules(records)}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tight := api.Limits{ConsumerKeysPerApp: 1, ConsumerKeysPerAccount: 100, AlertRuleLimitPerApp: 100, AlertRuleLimitPerAccount: 100}
	var keyQuota *ConsumerKeyQuotaError
	if err := checkCloneIdentityQuotaTx(ctx, tx, clone, tight); !errors.As(err, &keyQuota) || keyQuota.Observed != 2 {
		t.Fatalf("over-cap copy = %v", err)
	}
	_ = tx.Rollback(ctx)
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	roomy := api.Limits{ConsumerKeysPerApp: 10, ConsumerKeysPerAccount: 100, AlertRuleLimitPerApp: 10, AlertRuleLimitPerAccount: 100}
	if err := checkCloneIdentityQuotaTx(ctx, tx, clone, roomy); err != nil {
		t.Fatal(err)
	}
	var result ProjectEnvironmentCloneResult
	if err := copyCapturedConsumersTx(ctx, tx, clone, stage, &result); err != nil {
		t.Fatal(err)
	}
	if err := copyCapturedAlertRulesTx(ctx, tx, clone, stage, &result); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if result.ConsumersCopied != 1 || result.ConsumerKeysCopied != 1 || result.AlertRulesCopied != 1 {
		t.Fatalf("result = %+v", result)
	}

	// The stage's key answers with production's prefix and secret.
	copied, err := s.ConsumerKeyForEnvironment(ctx, account.ID, app.ID, stage.ID, prefix)
	if err != nil || copied.ID == key.ID || copied.EnvironmentID != stage.ID || !bytes.Equal(copied.Hash, key.Hash) || copied.RevokedAt != nil {
		t.Fatalf("stage key = %+v, %v; production key %+v", copied, err, key)
	}
	var enabled bool
	if err := pool.QueryRow(ctx, `select enabled or observation_enabled from alert_rules where environment_id = $1 and name = $2`, stage.ID, rule.Name).Scan(&enabled); err != nil || enabled {
		t.Fatalf("stage rule enabled = %v, %v; want a paused copy", enabled, err)
	}

	verify := func() error { return verifyCloneConsumerRecordDB(ctx, pool, record, mustPgUUID(stage.ID)) }
	if err := verify(); err != nil {
		t.Fatalf("publication of the copy: %v", err)
	}
	if err := verifyCloneAlertRuleRecordDB(ctx, pool, record, mustPgUUID(stage.ID)); err != nil {
		t.Fatalf("publication of the paused rules: %v", err)
	}

	// The revocation cascade triggers.
	if _, err := s.RevokeConsumerKey(ctx, account.ID, key.ID); err != nil {
		t.Fatal(err)
	}
	if k, err := s.ConsumerKeyForEnvironment(ctx, account.ID, app.ID, stage.ID, prefix); err != nil || k.RevokedAt == nil {
		t.Fatalf("stage key after production's revocation = %+v, %v", k, err)
	}
	if err := verify(); err != nil {
		t.Fatalf("publication after the source key's revocation: %v", err)
	}
	if _, err := s.RevokeAPIConsumer(ctx, account.ID, consumer.ID); err != nil {
		t.Fatal(err)
	}
	staged, err := s.ListAPIConsumersInEnvironment(ctx, account.ID, app.ID, "staging")
	if err != nil || len(staged) != 1 || staged[0].Active() {
		t.Fatalf("stage consumers after production's revocation = %+v, %v", staged, err)
	}
	if err := verify(); err != nil {
		t.Fatalf("publication after the source consumer's revocation: %v", err)
	}
}
