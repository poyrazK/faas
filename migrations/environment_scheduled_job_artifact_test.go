//go:build !no_pg

package migrations_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestScheduledArtifactReferenceIdentityAndReplay(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := t.Context()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	environment, app, id := "018f22b4-1d51-7d55-a5d8-fdc06ec7c660", "018f22b4-1d51-7d55-a5d8-fdc06ec7c661", "018f22b4-1d51-7d55-a5d8-fdc06ec7c663"
	prefix := "gregale-source://" + environment + "/" + app + "/"
	hash := strings.Repeat("a", 64)
	for _, tc := range []struct {
		name, kind, artifact, hash string
		revision                   any
		want                       string
	}{
		{"signed source", "source", id, hash, nil, prefix + "artifact/" + id + "/" + hash},
		{"signed dockerfile", "dockerfile", id, hash, nil, prefix + "artifact/" + id + "/" + hash},
		{"legacy source", "source", "", "", strings.Repeat("c", 40), prefix + strings.Repeat("c", 40)},
		{"sha256 commit", "source", "", "", strings.Repeat("c", 64), prefix + strings.Repeat("c", 64)},
		{"mixed commit and artifact", "source", id, hash, strings.Repeat("c", 40), ""},
		{"missing hash", "source", id, "", nil, ""},
		{"missing id", "source", "", hash, nil, ""},
		{"invalid id", "source", "bad", hash, nil, ""},
		{"nil id", "source", "00000000-0000-0000-0000-000000000000", hash, nil, ""},
		{"uppercase hash", "source", id, strings.Repeat("A", 64), nil, ""},
		{"function schedule", "function", id, hash, nil, ""},
		{"missing source kind", "", "", "", strings.Repeat("c", 40), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := map[string]string{"kind": tc.kind}
			if tc.artifact != "" {
				source["build_artifact_id"] = tc.artifact
			}
			if tc.hash != "" {
				source["build_artifact_hash"] = tc.hash
			}
			raw, _ := json.Marshal(source)
			var actual string
			if err := pool.QueryRow(ctx, `SELECT coalesce(environment_workload_source_job_reference($1::uuid,$2::uuid,$3::jsonb,$4::text),'')`, environment, app, raw, tc.revision).Scan(&actual); err != nil || actual != tc.want {
				t.Fatalf("reference=%q want=%q: %v", actual, tc.want, err)
			}
		})
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// A later fail-closed refinement must survive replay of this migration.
	if _, err = tx.Exec(ctx, `CREATE OR REPLACE FUNCTION environment_workload_source_job_deployment_matches(target_job uuid,target_deployment uuid) RETURNS boolean LANGUAGE sql AS 'SELECT false'`); err != nil {
		t.Fatal(err)
	}
	const definition = `SELECT pg_get_functiondef('environment_workload_source_job_deployment_matches(uuid,uuid)'::regprocedure)`
	var before, after string
	if err = tx.QueryRow(ctx, definition).Scan(&before); err != nil {
		t.Fatal(err)
	}
	raw, err := migrations.FS.ReadFile("20261017115917804_environment_scheduled_job_artifact_reuse.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, strings.Split(string(raw), "-- +goose Down")[0]); err != nil {
		t.Fatal("adapter replay", err)
	}
	if err = tx.QueryRow(ctx, definition).Scan(&after); err != nil || before != after {
		t.Fatal("replay replaced later guard", err)
	}
}
