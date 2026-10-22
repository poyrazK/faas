//go:build !no_pg

package deployreadiness

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261019120041
func TestReadinessPostgresReadOnly(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	writer := state.NewPgStore(pool)
	account, project, app := seedReadiness(t, writer)
	must(t, writer.UpsertAppEnvInScope(t.Context(), account.ID, app.ID, "production", "REGION", "production-only"))
	config := pool.Config()
	config.ConnConfig.RuntimeParams["default_transaction_read_only"] = "on"
	readPool, err := pgxpool.NewWithConfig(t.Context(), config)
	must(t, err)
	t.Cleanup(readPool.Close)
	store := state.NewPgStore(readPool)
	request := readinessRequest()
	request.Workloads[0].Workload.EnvKeys = []string{"REGION"}
	result := Check(t.Context(), store, account, project.Slug, request, Config{})
	if result.Status != "ready" || len(result.StateHash) != 64 {
		t.Fatalf("read-only PostgreSQL check failed: %+v", result)
	}
	request.ExpectedStateHash = result.StateHash
	must(t, writer.UpsertAppEnvInScope(t.Context(), account.ID, app.ID, "production", "REGION", "changed"))
	if result = Check(t.Context(), store, account, project.Slug, request, Config{}); result.Status != "blocked" || countCode(result, "deployment_inputs_changed") != 1 {
		t.Fatal("PostgreSQL input change was not detected", result)
	}
}
