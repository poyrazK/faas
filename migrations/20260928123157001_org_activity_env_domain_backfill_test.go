//go:build !no_pg

package migrations_test

import (
	"context"
	"io/fs"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_OrgActivityEnvDomainBackfill(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}

	ownerID := seedAccount(t, ctx, pool)
	actorID := seedAccount(t, ctx, pool)
	orgID := seedPersonalOrg(t, ctx, pool, ownerID)
	appID := seedApp(t, ctx, pool, ownerID)
	if _, err := pool.Exec(ctx, `UPDATE apps SET org_id = $2 WHERE id = $1`, appID, orgID); err != nil {
		t.Fatalf("set app organization: %v", err)
	}
	missingOrgAppID := seedApp(t, ctx, pool, ownerID)

	_, err := pool.Exec(ctx, `
		INSERT INTO events (at, actor, kind, subject, data) VALUES
		  (now() - interval '4 days', 'apid', 'env.set', $1::uuid,
		   jsonb_build_object('app_id', $2::text, 'scope', 'production', 'name', 'DATABASE_URL',
		     'value', 'postgres://must-not-copy', 'actor_ip', '203.0.113.21', 'token', 'hidden')),
		  (now() - interval '3 days', 'apid', 'env.deleted', $1::uuid,
		   jsonb_build_object('app_id', $2::text, 'name', 'OLD_TOKEN', 'value', 'must-not-copy')),
		  (now() - interval '2 days', 'apid', 'domain.added', $1::uuid,
		   jsonb_build_object('app_id', $2::text, 'domain', 'production.example.com',
		     'environment', 'production', 'provider_payload', 'must-not-copy')),
		  (now() - interval '1 day', 'apid', 'domain.removed', $1::uuid,
		   jsonb_build_object('app_id', $2::text, 'domain', 'old.example.com', 'client_ip', '203.0.113.22')),
		  (now() - interval '20 minutes', 'apid', 'env.set', $1::uuid,
		   jsonb_build_object('app_id', $2::text, 'name', 'AFTER_LIVE', 'value', 'must-not-copy')),
		  (now() - interval '10 minutes', 'apid', 'domain.added', $1::uuid,
		   jsonb_build_object('app_id', $2::text, 'domain', 'after.example.com')),
		  (now() - interval '3 days', 'apid', 'env.set', $1::uuid,
		   jsonb_build_object('app_id', $3::text, 'name', 'NO_ORG')),
		  (now() - interval '3 days', 'apid', 'env.set', $1::uuid,
		   jsonb_build_object('app_id', 'not-a-uuid', 'name', 'MALFORMED'))`,
		actorID, appID, missingOrgAppID)
	if err != nil {
		t.Fatalf("seed legacy env/domain events: %v", err)
	}

	// Current emitters persist the same mutation to org_activity or its
	// outbox. These timestamps define the per-kind cutover and prevent the
	// corresponding legacy audit rows from appearing a second time.
	if _, err := pool.Exec(ctx, `
		INSERT INTO org_activity (
			org_id, occurred_at, kind, actor_type, actor_label, resource_type,
			resource_id, resource_label, app_id, data, source_type, source_id
		) VALUES (
			$1, now() - interval '1 hour', 'env.set', 'user', 'Live actor',
			'environment_variable', 'default:AFTER_LIVE', 'AFTER_LIVE', $2::uuid,
			'{"scope":"default"}'::jsonb, 'env.set', 'live-env-set'
		)`, orgID, appID); err != nil {
		t.Fatalf("seed live env activity: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO org_activity_outbox (org_id, source_type, source_id, activity, created_at)
		VALUES ($1, 'domain.added', 'pending-live-domain',
		        jsonb_build_object('Kind', 'domain.added', 'OccurredAt', now() - interval '30 minutes'),
		        now() - interval '30 minutes')`, orgID); err != nil {
		t.Fatalf("seed pending live domain activity: %v", err)
	}

	source, err := fs.ReadFile(migrations.FS, "20260928123157001_org_activity_env_domain_backfill.sql")
	if err != nil {
		t.Fatalf("read embedded migration: %v", err)
	}
	upParts := strings.SplitN(string(source), "-- +goose Up", 2)
	if len(upParts) != 2 {
		t.Fatal("migration has no goose Up section")
	}
	upSQL := strings.SplitN(upParts[1], "-- +goose Down", 2)[0]
	if _, err := pool.Exec(ctx, upSQL); err != nil {
		t.Fatalf("replay env/domain activity backfill: %v", err)
	}
	if _, err := pool.Exec(ctx, upSQL); err != nil {
		t.Fatalf("replay env/domain activity backfill a second time: %v", err)
	}

	var imported int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM org_activity
		 WHERE org_id = $1 AND app_id = $2::uuid AND source_type = 'legacy.events'`,
		orgID, appID).Scan(&imported); err != nil {
		t.Fatalf("count imported legacy activity: %v", err)
	}
	if imported != 4 {
		t.Fatalf("imported legacy event count = %d, want 4 safe pre-cutover rows", imported)
	}

	var kind, actorType, actorLabel, resourceType, resourceID, resourceLabel string
	var data []byte
	err = pool.QueryRow(ctx, `
		SELECT kind, actor_type, actor_label, resource_type, resource_id,
		       resource_label, data
		  FROM org_activity
		 WHERE org_id = $1 AND app_id = $2::uuid
		   AND source_type = 'legacy.events' AND kind = 'env.set'`, orgID, appID).Scan(
		&kind, &actorType, &actorLabel, &resourceType, &resourceID, &resourceLabel, &data)
	if err != nil {
		t.Fatalf("read imported env activity: %v", err)
	}
	if kind != "env.set" || actorType != "user" || actorLabel != actorID+"@example.test" ||
		resourceType != "environment_variable" || resourceID != "production:DATABASE_URL" ||
		resourceLabel != "DATABASE_URL" {
		t.Errorf("imported env activity = %q %q/%q %q/%q/%q", kind, actorType, actorLabel, resourceType, resourceID, resourceLabel)
	}
	var scope string
	if err := pool.QueryRow(ctx, `
		SELECT data->>'scope' FROM org_activity
		 WHERE org_id = $1 AND source_type = 'legacy.events' AND kind = 'env.set'`, orgID).Scan(&scope); err != nil {
		t.Fatalf("read safe env scope: %v", err)
	}
	if scope != "production" {
		t.Errorf("env scope = %q, want production", scope)
	}
	for _, forbidden := range []string{"must-not-copy", "203.0.113.", "postgres://", "provider_payload", "client_ip", "token"} {
		if strings.Contains(string(data), forbidden) {
			t.Errorf("unsafe legacy data %q copied into env activity %s", forbidden, data)
		}
	}

	var defaultScope string
	if err := pool.QueryRow(ctx, `
		SELECT data->>'scope' FROM org_activity
		 WHERE org_id = $1 AND source_type = 'legacy.events' AND kind = 'env.deleted'`, orgID).Scan(&defaultScope); err != nil {
		t.Fatalf("read defaulted env scope: %v", err)
	}
	if defaultScope != "default" {
		t.Errorf("legacy env delete scope = %q, want default", defaultScope)
	}

	var domainLabel, domainEnvironment string
	if err := pool.QueryRow(ctx, `
		SELECT resource_label, data->>'environment'
		  FROM org_activity
		 WHERE org_id = $1 AND source_type = 'legacy.events' AND kind = 'domain.added'`, orgID).Scan(
		&domainLabel, &domainEnvironment); err != nil {
		t.Fatalf("read imported domain activity: %v", err)
	}
	if domainLabel != "production.example.com" || domainEnvironment != "production" {
		t.Errorf("domain activity = %q in environment %q, want production.example.com in production", domainLabel, domainEnvironment)
	}
	var domainData []byte
	if err := pool.QueryRow(ctx, `
		SELECT data FROM org_activity
		 WHERE org_id = $1 AND source_type = 'legacy.events' AND kind = 'domain.added'`, orgID).Scan(&domainData); err != nil {
		t.Fatalf("read safe domain data: %v", err)
	}
	for _, forbidden := range []string{"must-not-copy", "provider_payload"} {
		if strings.Contains(string(domainData), forbidden) {
			t.Errorf("unsafe legacy data %q copied into domain activity %s", forbidden, domainData)
		}
	}

	var suppressed int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM org_activity
		 WHERE org_id = $1 AND source_type = 'legacy.events'
		   AND resource_label IN ('AFTER_LIVE', 'after.example.com')`, orgID).Scan(&suppressed); err != nil {
		t.Fatalf("count post-cutover legacy duplicates: %v", err)
	}
	if suppressed != 0 {
		t.Errorf("imported %d post-cutover rows already represented by live activity", suppressed)
	}
}
