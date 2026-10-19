//go:build !no_pg

package migrations_test

import (
	"encoding/json"
	"errors"
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMigrationBuildDependencyProgressConstraints(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	defer pool.Close()
	ctx := t.Context()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	account := seedAccount(t, ctx, pool)
	app := seedApp(t, ctx, pool, account)
	deployment := seedDeployment(t, ctx, pool, app, "pending")
	gate := state.BuildDependencyGate{Dependencies: []state.DeploymentDependencyPin{{Service: "api", AppID: app, DeploymentID: deployment}}, Status: "waiting", DeadlineAt: time.Now().UTC().Add(api.ProjectMigrationBuildWaitTimeout)}
	raw, err := json.Marshal(gate)
	if err != nil {
		t.Fatal(err)
	}
	var valid map[string]any
	if err := json.Unmarshal(raw, &valid); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO deployment_build_dependency_gates(deployment_id,progress) VALUES ($1,$2::jsonb)`, deployment, raw); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"waiting", "ready", "failed"} {
		if _, err := pool.Exec(ctx, `UPDATE deployment_build_dependency_gates SET progress=jsonb_set(progress,'{status}',to_jsonb($2::text)) WHERE deployment_id=$1`, deployment, status); err != nil {
			t.Fatal(err)
		}
	}
	cases := map[string]map[string]any{"missing fields": {}, "empty dependencies": maps.Clone(valid), "too many dependencies": maps.Clone(valid), "unknown status": maps.Clone(valid), "oversized": maps.Clone(valid)}
	cases["empty dependencies"]["dependencies"] = []any{}
	cases["too many dependencies"]["dependencies"] = make([]any, api.ProjectMigrationMaxServices+1)
	cases["unknown status"]["status"] = "unrecognized"
	cases["oversized"]["blocker"] = strings.Repeat("x", api.ProjectMigrationBuildGateMaxBytes)
	for _, key := range []string{"dependencies", "status", "deadline_at", "check_after"} {
		missing := maps.Clone(valid)
		delete(missing, key)
		cases["missing "+key] = missing
		null := maps.Clone(valid)
		null[key] = nil
		cases["null "+key] = null
	}
	for name, progress := range cases {
		t.Run(name, func(t *testing.T) {
			raw, err := json.Marshal(progress)
			if err != nil {
				t.Fatal(err)
			}
			_, err = pool.Exec(ctx, `UPDATE deployment_build_dependency_gates SET progress=$2::jsonb WHERE deployment_id=$1`, deployment, raw)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
				t.Fatalf("malformed gate was not rejected by its constraint: %v", err)
			}
		})
	}
}
