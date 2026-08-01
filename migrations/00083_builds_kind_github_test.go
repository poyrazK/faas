//go:build !no_pg

// Migration-apply test for 00083 (githubd build_kind='github', issue
// #432 phase 5 follow-up). Pins the CHECK relaxation:
//
//  1. The migration set applies cleanly through 00083.
//  2. The CHECK accepts the new 'github' value (kind='github' inserts
//     cleanly into the builds table).
//  3. The CHECK rejects other values (e.g. 'cli') to pin that the
//     closed vocabulary hasn't regressed to "any text".
//  4. Replay-safe: a second MigrateUp is a no-op (the migration drops
//     and re-adds the CHECK via `if exists`, idempotently).
//
// Slot note: HEAD is at 00082 (apps scaling policy, sibling PR), so
// 00083 is the next free slot at PR creation time. If a sibling PR
// grabs 00083 first, renumber per `migrations/README.md` and update
// this test's filename + ApplyUp range.
package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_00083_BuildsKindGitHub(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	// (1) Apply through 00083.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v (regression: missing migration slot between 1 and 83)", err)
	}

	// (2) 'github' kind is accepted. We seed an account + app so the
	// fk chain doesn't trip the inserts, then write a build row with
	// kind='github'. This is the load-bearing tripwire — pre-00083,
	// the CHECK rejects this with a constraint violation.
	if _, err := pool.Exec(ctx, `
		insert into accounts (id, email, plan, created_at)
		values ('00000000-0000-0000-0000-000000000083',
		        'builds-kind-github-test@example.com', 'hobby', now())
		on conflict (id) do nothing
	`); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into apps (id, account_id, slug, type, ram_mb, max_concurrency, idle_timeout_s, status, created_at)
		values ('00000000-0000-0000-0000-000000000183',
		        '00000000-0000-0000-0000-000000000083',
		        'builds-kind-github-test-app', 'function', 256, 1, 30, 'active', now())
		on conflict (id) do nothing
	`); err != nil {
		t.Fatalf("seed app: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into deployments (id, app_id, kind, status, source_url, commit_sha, created_at)
		values ('00000000-0000-0000-0000-000000000283',
		        '00000000-0000-0000-0000-000000000183',
		        'github', 'pending', 'https://github.com/example/repo@abc123', 'abc123', now())
		on conflict (id) do nothing
	`); err != nil {
		t.Fatalf("seed deployment: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into builds (id, deployment_id, kind, status, created_at)
		values ('00000000-0000-0000-0000-000000000383',
		        '00000000-0000-0000-0000-000000000283',
		        'github', 'pending', now())
	`); err != nil {
		t.Fatalf("insert build kind=github: %v (regression: CHECK did not accept the new value)", err)
	}

	// (3) Closed vocabulary is preserved: an unknown kind is still
	// rejected. This pins that the relaxation didn't open the column
	// to "any text".
	_, err := pool.Exec(ctx, `
		insert into builds (id, deployment_id, kind, status, created_at)
		values ('00000000-0000-0000-0000-000000000483',
		        '00000000-0000-0000-0000-000000000283',
		        'cli', 'pending', now())
	`)
	if err == nil {
		t.Errorf("builds.kind='cli' was accepted; CHECK did not preserve the closed vocabulary")
	} else if !strings.Contains(err.Error(), "builds_kind_check") {
		t.Errorf("expected CHECK violation, got: %v", err)
	}

	// (4) Replay safety.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("replay-safety: second MigrateUp failed: %v", err)
	}
}