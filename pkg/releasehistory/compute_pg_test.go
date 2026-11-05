//go:build !no_pg

package releasehistory_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/releasehistory"
	"github.com/onebox-faas/faas/pkg/releaseinstall"
)

// installerPool returns a pool on owner's database whose sessions run as a
// fresh role shaped like faas_compute: row DML on every table, but no writes
// to the release history (the migration and runtime-roles.sql revoke them).
func installerPool(t *testing.T, owner *pgxpool.Pool) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	name := "faas_rt_" + hex.EncodeToString(suffix)
	role := pgx.Identifier{name}.Sanitize()
	var current string
	if err := owner.QueryRow(ctx, "SELECT current_schema()").Scan(&current); err != nil {
		t.Fatal(err)
	}
	schema := pgx.Identifier{current}.Sanitize()
	t.Cleanup(func() {
		_, _ = owner.Exec(context.Background(), "DROP OWNED BY "+role)
		_, _ = owner.Exec(context.Background(), "DROP ROLE "+role)
	})
	for _, stmt := range []string{
		"CREATE ROLE " + role + " NOLOGIN",
		"GRANT " + role + " TO CURRENT_USER",
		"GRANT USAGE ON SCHEMA " + schema + " TO " + role,
		"GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA " + schema + " TO " + role,
		"GRANT USAGE, SELECT, UPDATE ON ALL SEQUENCES IN SCHEMA " + schema + " TO " + role,
		"REVOKE INSERT, UPDATE, DELETE ON platform_release_activations FROM " + role,
	} {
		if _, err := owner.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	cfg := owner.Config().Copy()
	cfg.ConnConfig.RuntimeParams["role"] = name
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// A compute host changes release through `gregalectl release install`, which
// stamps compute_nodes.release_id as a runtime role. Each change is recorded
// as a finished deploy with the release it replaced; the installer itself
// still cannot write the history.
func TestComputeReleaseInstallIsRecorded(t *testing.T) {
	ctx := context.Background()
	owner := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, owner); err != nil {
		t.Fatal(err)
	}
	installer := installerPool(t, owner)
	nodes := releaseinstall.NewStore(installer)
	shaA, shaB := strings.Repeat("a", 40), strings.Repeat("b", 40)
	manifest := "sha256:" + strings.Repeat("c", 64)
	for _, sha := range []string{shaA, shaA, shaB} { // the repeat is no change
		if _, err := nodes.UpsertComputeNode(ctx, "node-1.faas", sha, manifest); err != nil {
			t.Fatalf("install %s: %v", sha[:4], err)
		}
	}
	if _, err := installer.Exec(ctx, `INSERT INTO platform_release_activations (host, kind, release_id, actor, status, started_at, finished_at)
		VALUES ('forged', 'deploy', 'x', 'me', 'succeeded', now(), now())`); err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("installer wrote the history directly: %v", err)
	}

	rows, err := releasehistory.NewStore(owner).List(ctx, "node-1.faas", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("recorded %d activations, want 2: %+v", len(rows), rows)
	}
	for i, want := range []struct{ release, previous string }{{shaB, shaA}, {shaA, ""}} {
		got := rows[i]
		if got.ReleaseID != want.release || got.PreviousReleaseID != want.previous || got.CommitSHA != want.release ||
			got.Kind != releasehistory.KindDeploy || got.Status != releasehistory.StatusSucceeded ||
			!strings.HasPrefix(got.Actor, "release install as ") || got.FinishedAt == nil {
			t.Fatalf("activation %d = %+v, want %s over %q", i, got, want.release[:4], want.previous)
		}
	}
}
