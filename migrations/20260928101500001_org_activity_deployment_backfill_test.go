//go:build !no_pg

package migrations_test

import (
	"context"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_OrgActivityDeploymentBackfill(t *testing.T) {
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
	deploymentID := uuid.NewString()
	missingOrgAppID := seedApp(t, ctx, pool, ownerID)
	missingOrgDeploymentID := uuid.NewString()
	unknownActorDeploymentID := uuid.NewString()
	githubDeploymentID := uuid.NewString()
	apiKeyDeploymentID := uuid.NewString()
	liveDeploymentID := uuid.NewString()

	_, err := pool.Exec(ctx, `
		INSERT INTO events (at, actor, kind, subject, data) VALUES
		  (now() - interval '3 days', 'dashboard:' || $1::text, 'deploy.source_ref', $2::uuid,
		   jsonb_build_object(
		     'app_id', $3::text, 'deployment_id', $4::text,
		     'actor_user_id', $1::text, 'actor_via', 'dashboard',
		     'actor_ip', '203.0.113.10', 'repo', 'secret-owner/private-repo',
		     'ref', 'internal/release', 'source_sha', 'abc123', 'access_token', 'must-not-copy')),
		  (now() - interval '2 days', 'apid', 'app.deployed', $2::uuid,
		   jsonb_build_object('app_id', $3::text, 'deployment_id', $4::text,
		     'actor_ip', '203.0.113.11', 'image', 'private.example/image:tag')),
		  (now() - interval '1 day', 'apid', 'deploy.local_tarball', $2::uuid,
		   jsonb_build_object('app_id', $3::text, 'deployment_id', $5::text,
		     'repo', 'private-repo', 'actor_ip', '203.0.113.12')),
		  (now(), 'apid', 'app.deployed', $2::uuid,
		   jsonb_build_object('app_id', $6::text, 'deployment_id', $7::text,
		     'actor_ip', '203.0.113.13')),
		  (now(), 'github:alice', 'deploy.source_ref', $2::uuid,
		   jsonb_build_object('app_id', $3::text, 'deployment_id', $8::text,
		     'actor_via', 'github', 'actor_pusher', 'alice', 'ref', 'private/ref')),
		  (now(), 'api:service', 'deploy.local_tarball', $2::uuid,
		   jsonb_build_object('app_id', $3::text, 'deployment_id', $9::text,
		     'actor_via', 'api', 'actor_user_id', $1::text, 'actor_ip', '203.0.113.14')),
		  (now(), 'apid', 'app.deployed', $2::uuid,
		   jsonb_build_object('app_id', $3::text, 'deployment_id', $10::text)),
		  (now(), 'apid', 'app.deployed', $2::uuid,
		   jsonb_build_object('app_id', $3::text, 'deployment_id', 'not-a-uuid'))`,
		actorID, ownerID, appID, deploymentID, unknownActorDeploymentID, missingOrgAppID,
		missingOrgDeploymentID, githubDeploymentID, apiKeyDeploymentID, liveDeploymentID)
	if err != nil {
		t.Fatalf("seed legacy deployment events: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO org_activity (
			org_id, occurred_at, kind, actor_type, actor_label, resource_type,
			resource_id, resource_label, app_id, deployment_id, data,
			source_type, source_id
		) VALUES (
			$1, now(), 'deploy.requested', 'user', 'Existing live activity', 'app',
			$2::uuid::text, 'Current app slug', $2::uuid, $3::uuid,
			'{"phase":"requested","source":"image"}'::jsonb,
			'deployment.requested', $3::uuid::text
		)`, orgID, appID, liveDeploymentID); err != nil {
		t.Fatalf("seed existing live activity: %v", err)
	}

	// Re-execute the exact embedded Up body after seeding (MigrateUp ran it
	// once against an empty table). This validates the real migration mapping
	// and gives the backfill's insert path an explicit replay check.
	source, err := fs.ReadFile(migrations.FS, "20260928101500001_org_activity_deployment_backfill.sql")
	if err != nil {
		t.Fatalf("read embedded migration: %v", err)
	}
	upParts := strings.SplitN(string(source), "-- +goose Up", 2)
	if len(upParts) != 2 {
		t.Fatal("migration has no goose Up section")
	}
	upSQL := strings.SplitN(upParts[1], "-- +goose Down", 2)[0]
	if _, err := pool.Exec(ctx, upSQL); err != nil {
		t.Fatalf("replay deployment activity backfill: %v", err)
	}
	if _, err := pool.Exec(ctx, upSQL); err != nil {
		t.Fatalf("replay deployment activity backfill a second time: %v", err)
	}

	var count int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM org_activity
		 WHERE org_id = $1 AND source_type = 'deployment.requested'
		   AND source_id = $2`, orgID, deploymentID).Scan(&count); err != nil {
		t.Fatalf("count imported image/source-ref event: %v", err)
	}
	if count != 1 {
		t.Fatalf("imported request count = %d, want one deduplicated row", count)
	}
	var existingLabel string
	if err := pool.QueryRow(ctx, `
		SELECT actor_label FROM org_activity
		 WHERE org_id = $1 AND source_type = 'deployment.requested'
		   AND source_id = $2`, orgID, liveDeploymentID).Scan(&existingLabel); err != nil {
		t.Fatalf("read existing live activity: %v", err)
	}
	if existingLabel != "Existing live activity" {
		t.Errorf("legacy import replaced existing live row actor label with %q", existingLabel)
	}

	var occurredAt time.Time
	var kind, actorType, actorLabel, resourceLabel string
	var actorAccountID *string
	var data []byte
	err = pool.QueryRow(ctx, `
		SELECT occurred_at, kind, actor_type, actor_account_id::text, actor_label,
		       resource_label, data
		  FROM org_activity
		 WHERE org_id = $1 AND source_type = 'deployment.requested'
		   AND source_id = $2`, orgID, deploymentID).Scan(
		&occurredAt, &kind, &actorType, &actorAccountID, &actorLabel, &resourceLabel, &data)
	if err != nil {
		t.Fatalf("read imported activity: %v", err)
	}
	if kind != "deploy.requested" || actorType != "user" || actorLabel != actorID+"@example.test" ||
		actorAccountID == nil || *actorAccountID != actorID || resourceLabel == "" {
		t.Errorf("imported activity identity = kind:%q actor:%q/%q/%v resource:%q", kind, actorType, actorLabel, actorAccountID, resourceLabel)
	}
	if time.Since(occurredAt) < 48*time.Hour || time.Since(occurredAt) > 96*time.Hour {
		t.Errorf("occurred_at = %s, want the original ~3-day-old event time", occurredAt)
	}
	for _, forbidden := range []string{"203.0.113.", "private-repo", "internal/release", "abc123", "must-not-copy", "private.example"} {
		if strings.Contains(string(data), forbidden) {
			t.Errorf("unsafe legacy field %q copied into activity data %s", forbidden, data)
		}
	}
	if !strings.Contains(string(data), `"phase":"requested"`) || !strings.Contains(string(data), `"source":"source_ref"`) {
		t.Errorf("safe metadata = %s, want request phase and source_ref source", data)
	}

	assertImportedCount := func(want int, deploymentID string) {
		t.Helper()
		var got int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM org_activity WHERE deployment_id = $1`, deploymentID).Scan(&got); err != nil {
			t.Fatalf("count deployment %s activity: %v", deploymentID, err)
		}
		if got != want {
			t.Errorf("deployment %s activity count = %d, want %d", deploymentID, got, want)
		}
	}
	assertImportedCount(0, missingOrgDeploymentID)
	var malformedSourceCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM org_activity
		 WHERE org_id = $1 AND source_type = 'deployment.requested'
		   AND source_id = 'not-a-uuid'`, orgID).Scan(&malformedSourceCount); err != nil {
		t.Fatalf("count malformed deployment events: %v", err)
	}
	if malformedSourceCount != 0 {
		t.Errorf("malformed deployment event produced %d activity rows, want 0", malformedSourceCount)
	}

	var unknownActorType, unknownActorLabel string
	if err := pool.QueryRow(ctx, `
		SELECT actor_type, actor_label FROM org_activity
		 WHERE org_id = $1 AND source_type = 'deployment.requested'
		   AND source_id = $2`, orgID, unknownActorDeploymentID).Scan(&unknownActorType, &unknownActorLabel); err != nil {
		t.Fatalf("read unknown-actor activity: %v", err)
	}
	if unknownActorType != "system" || unknownActorLabel != "Unknown (legacy event)" {
		t.Errorf("unknown actor = %q/%q, want explicit unknown legacy attribution", unknownActorType, unknownActorLabel)
	}

	for _, tc := range []struct {
		deploymentID string
		actorType    string
		actorLabel   string
	}{
		{githubDeploymentID, "github", "GitHub Actions"},
		{apiKeyDeploymentID, "api_key", "API key"},
	} {
		var gotType, gotLabel string
		var gotActorAccountID *string
		var gotData []byte
		if err := pool.QueryRow(ctx, `
			SELECT actor_type, actor_account_id::text, actor_label, data
			  FROM org_activity
			 WHERE org_id = $1 AND source_type = 'deployment.requested'
			   AND source_id = $2`, orgID, tc.deploymentID).Scan(
			&gotType, &gotActorAccountID, &gotLabel, &gotData); err != nil {
			t.Fatalf("read %s actor activity: %v", tc.actorType, err)
		}
		if gotType != tc.actorType || gotLabel != tc.actorLabel {
			t.Errorf("%s actor = %q/%q, want %q/%q", tc.deploymentID, gotType, gotLabel, tc.actorType, tc.actorLabel)
		}
		if gotActorAccountID != nil {
			t.Errorf("%s activity actor account id = %q, want nil for external/key actor", tc.deploymentID, *gotActorAccountID)
		}
		if strings.Contains(string(gotData), "alice") || strings.Contains(string(gotData), "private/ref") || strings.Contains(string(gotData), "203.0.113.") {
			t.Errorf("%s unsafe actor/source metadata copied into %s", tc.deploymentID, gotData)
		}
	}
}
