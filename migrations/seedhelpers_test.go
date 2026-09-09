//go:build !no_pg

// Shared FK-parent seed helpers for the Postgres-backed migration tests.
//
// Why this file exists: the migration tests seed child rows (api_keys,
// app_envs, app_secrets, deployments, instances) to pin a migration's
// behaviour. Every one of those tables has grown FKs and NOT NULL
// columns since its test was written, so tests that inlined a literal
// UUID for a parent id (or relied on a nullable column) rot the moment
// a later migration tightens the schema. Seeding the real parent row
// through one helper keeps that class of rot in a single place: when a
// parent table gains a NOT NULL column, only this file changes.
//
// Argument order mirrors the pre-existing seedAccount helper in
// 00074_projects_and_workloads_test.go (t, ctx, pool, ...) — reuse
// seedAccount from here rather than adding a second accounts seeder.
//
// Build tag matches the rest of the migration tests; set
// FAAS_SKIP_PG_TESTS=1 to skip locally (see migrations/README.md).
package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// slugFragment returns a 12-character lowercase hex fragment of a fresh
// UUID. Both `orgs.slug` (orgs_slug_shape: ^[a-z0-9][a-z0-9-]{1,30}[a-z0-9]$)
// and the globally-unique `apps.slug` need a short, collision-free,
// lowercase-alphanumeric token; this is it.
func slugFragment() string {
	return strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
}

// seedOrg inserts a minimal shared (non-personal) org and returns its id.
// This is the FK parent for `api_keys.org_id` (api_keys_org_id_fkey,
// ON DELETE RESTRICT) and for the nullable `org_id` on the section-B
// tenant tables. A shared org carries personal_owner_account_id = NULL,
// which is what orgs_personal_owner_link requires when personal_org is
// false — do not "simplify" this into a personal org.
func seedOrg(t *testing.T, ctx context.Context, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(ctx, `
		insert into orgs (slug, name) values ($1, 'Seeded Org')
		returning id::text
	`, "org-"+slugFragment()).Scan(&id); err != nil {
		t.Fatalf("seed orgs row: %v", err)
	}
	return id
}

// seedPersonalOrg inserts the personal org owned by accountID plus its
// owner membership, mirroring the two INSERT bodies of
// 00105_personal_org_backfill.sql verbatim (slug shape included). It
// returns the org id.
//
// The partial unique orgs_one_personal_per_account_uniq (00099)
// guarantees at most one personal org per account, so this is the row
// every account-scoped backfill — 00134's api_keys.org_id stamp among
// them — resolves to.
func seedPersonalOrg(t *testing.T, ctx context.Context, pool *pgxpool.Pool, accountID string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(ctx, `
		INSERT INTO orgs (
			id, slug, name, personal_org, personal_owner_account_id,
			plan, status, created_at, updated_at
		)
		SELECT
			gen_random_uuid(),
			'u-' || substring(replace(a.id::text, '-', '') from 1 for 12),
			'Personal', true, a.id, a.plan, a.status, a.created_at, now()
		FROM accounts a
		WHERE a.id = $1
		RETURNING id::text
	`, accountID).Scan(&id); err != nil {
		t.Fatalf("seed personal org for account %s: %v", accountID, err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO org_memberships (org_id, account_id, role, invited_by_account_id)
		VALUES ($1, $2, 'owner', NULL)
		ON CONFLICT DO NOTHING
	`, id, accountID); err != nil {
		t.Fatalf("seed owner membership for account %s: %v", accountID, err)
	}
	return id
}

// seedApp inserts a minimal apps row owned by accountID and returns its
// id. It is the FK parent for deployments.app_id, instances.app_id, and
// the account/app pair on app_envs / app_secrets. The slug is randomised
// because apps.slug carries a GLOBAL unique constraint (apps_slug_key),
// not a per-account one.
func seedApp(t *testing.T, ctx context.Context, pool *pgxpool.Pool, accountID string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(ctx, `
		insert into apps (account_id, slug, type, ram_mb, max_concurrency,
		                  idle_timeout_s, status, created_at)
		values ($1, $2, 'function', 128, 1, 30, 'active', now())
		returning id::text
	`, accountID, "seed-app-"+slugFragment()).Scan(&id); err != nil {
		t.Fatalf("seed apps row for account %s: %v", accountID, err)
	}
	return id
}

// seedDeployment inserts a deployments row for appID with the requested
// status and returns its id. status is a parameter (not hardcoded to
// 'live') because deployments_app_scope_live_uniq is a PARTIAL unique on
// (app_id, scope) WHERE status = 'live' — a helper that always seeded
// 'live' would collide on the second call for one app.
func seedDeployment(t *testing.T, ctx context.Context, pool *pgxpool.Pool, appID, status string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(ctx, `
		insert into deployments (app_id, kind, image_digest, status, created_at)
		values ($1, 'image', $2, $3, now())
		returning id::text
	`, appID, "sha256:seed-"+slugFragment(), status).Scan(&id); err != nil {
		t.Fatalf("seed deployments row for app %s: %v", appID, err)
	}
	return id
}

// seedComputeNode inserts an active compute_nodes row and returns its
// id — the FK parent for instances.node_id. `active` is a GENERATED
// column (lifecycle = active|recovering), so lifecycle is what we set.
func seedComputeNode(t *testing.T, ctx context.Context, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(ctx, `
		insert into compute_nodes (name, target_url, vpcpus, mem_mb,
		                           max_concurrency, admission_ceiling_mb, lifecycle)
		values ($1, 'tcp://test:50051', 160, 56000, 200, 47600,
		        'active'::compute_node_lifecycle)
		returning id::text
	`, "seed-node-"+slugFragment()).Scan(&id); err != nil {
		t.Fatalf("seed compute_nodes row: %v", err)
	}
	return id
}
