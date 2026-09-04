//go:build !no_pg

// Replay-safety gate for migrations added by the current PR.
//
// The failure this prevents
// -------------------------
// CI always migrates a FRESH database, so every migration is only ever
// exercised against a schema that does not yet contain its objects. The DO
// box is not fresh: twice now its schema has been AHEAD of its goose ledger
// (DDL applied outside goose — manual psql, a restored dump, a
// partially-applied deploy), and the deploy died mid-apply:
//
//	00030_invocations:            relation "invocations" already exists     (42P07)
//	00053_deployments_source_url: column "source_url" ... already exists    (42701)
//
// Both were green in CI and both took cd-digitalocean red. 00053 was
// subsequently hand-hardened with ADD COLUMN IF NOT EXISTS (commit ed82cd6),
// but nothing stopped the NEXT migration from repeating it.
//
// Scope: only migrations the PR ADDS
// ----------------------------------
// This deliberately does not demand replay-safety of the whole history.
// 00001_init legitimately is not re-runnable (bare CREATE TABLE), and
// replaying from version 0 against a populated database is not a scenario
// anyone should survive. What matters is the tail: a migration that has not
// deployed everywhere yet is the one that will meet a drifted box on the next
// deploy. So CI passes the versions this PR adds and only those are replayed.
//
// Wiring: .github/workflows/ci.yml computes the added versions with
// `git diff --diff-filter=A` and exports FAAS_REPLAY_CHECK_VERSIONS. Unset
// (local `make test`, or a PR with no new migrations) => skip.
package migrations_test

import (
	"context"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestNewMigrationsAreReplaySafe(t *testing.T) {
	raw := strings.TrimSpace(os.Getenv("FAAS_REPLAY_CHECK_VERSIONS"))
	if raw == "" {
		t.Skip("FAAS_REPLAY_CHECK_VERSIONS unset (no new migrations in this diff); nothing to replay")
	}

	versions := parseVersions(t, raw)
	if len(versions) == 0 {
		t.Skip("no parseable versions in FAAS_REPLAY_CHECK_VERSIONS")
	}
	ctx := context.Background()
	pool := pgtest.Open(t) // t.Skip-friendly when Postgres is absent

	// 1. Bring the schema fully up, the way CI always has.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("initial MigrateUp: %v", err)
	}

	// 2. Drop exactly the ledger rows added by this PR, leaving their schema
	//    effects in place. With timestamp IDs, unrelated migrations may have
	//    higher or lower versions and must not be pulled into this replay.
	tag, err := pool.Exec(ctx, `DELETE FROM goose_db_version WHERE version_id = ANY($1::bigint[])`, versions)
	if err != nil {
		t.Fatalf("delete goose rows %v: %v", versions, err)
	}
	if got, want := tag.RowsAffected(), int64(len(versions)); got != want {
		t.Fatalf("deleted %d goose_db_version rows for %v, want %d — the version list does not match "+
			"what was applied, so this test would prove only part of the diff", got, versions, want)
	}

	// 3. Re-apply. Every migration this PR added must tolerate its own
	//    effects already being present.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf(`migration set %v is not replay-safe: %v

This migration fails when its objects already exist but goose has no row for
it. That is the state the DO box has reached twice (00030, 00053), and it
takes the deploy down mid-apply while CI stays green — CI only ever migrates
a fresh database.

Make the new migration re-runnable. The established shapes in this repo:
  CREATE TABLE IF NOT EXISTS / CREATE INDEX IF NOT EXISTS
  ALTER TABLE ... ADD COLUMN IF NOT EXISTS
  constraints + enum-value additions: guard with a DO block that checks
  pg_constraint / information_schema first (see 00053 for a worked example)

Do NOT edit an already-merged migration to fix this — migrations are
append-only (CLAUDE.md). Only the migration this PR adds should change.`, versions, err)
	}
}

// parseVersions turns legacy or timestamp version strings into sorted int64s.
func parseVersions(t *testing.T, raw string) []int64 {
	t.Helper()
	var out []int64
	for _, f := range strings.Split(raw, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		// Base 10 explicitly: "00054" must not be read as octal.
		v, err := strconv.ParseInt(strings.TrimLeft(f, "0"), 10, 64)
		if err != nil {
			t.Fatalf("FAAS_REPLAY_CHECK_VERSIONS: cannot parse %q: %v", f, err)
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
