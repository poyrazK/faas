//go:build !no_pg

// adr: 428 — the deployment index rollback never destroys verification history.
package migrations_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestDeploymentBindingVerificationMigrationRoundTrip(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	defer pool.Close()
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	account := seedAccount(t, ctx, pool)
	app := seedApp(t, ctx, pool, account)
	dep := seedDeployment(t, ctx, pool, app, "live")
	for _, binding := range []struct{ kind, command string }{{api.BindingTypeService, api.AppTaskServiceBindingProbeCommand}, {api.BindingTypePostgres, api.AppTaskPostgresBindingProbeCommand}, {api.BindingTypeObjectStorage, api.AppTaskObjectStorageBindingProbeCommand}} {
		pin, _ := json.Marshal(map[string]string{"type": binding.kind, "binding": "ASSETS", "revision": strings.Repeat("a", 64)})
		if _, err := pool.Exec(ctx, `INSERT INTO app_tasks(id,account_id,app_id,deployment_id,kind,command,command_shell,deployment_scope,artifact_key,image_digest,timeout_seconds,max_output_bytes,binding_verification) VALUES($1,$2,$3,$4,'manual',$5,false,'default','test/rootfs','sha256:test',15,4096,$6::jsonb)`, uuid.NewString(), account, app, dep, []string{binding.command, "ASSETS"}, string(pin)); err != nil {
			t.Fatal(err)
		}
	}
	source, err := os.ReadFile("20261002170059743_deployment_binding_verification.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, ok := strings.Cut(string(source), "-- +goose Down")
	if !ok {
		t.Fatal("missing rollback")
	}
	for _, step := range []struct {
		sql    string
		exists bool
	}{{down, false}, {up, true}} {
		if _, err := pool.Exec(ctx, step.sql); err != nil {
			t.Fatal(err)
		}
		var retained, pins int
		if err := pool.QueryRow(ctx, `SELECT count(*),count(binding_verification) FROM app_tasks WHERE app_id=$1`, app).Scan(&retained, &pins); err != nil || retained != 3 || pins != 3 {
			t.Fatalf("history=%d pins=%d err=%v", retained, pins, err)
		}
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT to_regclass('app_tasks_binding_verification_deployment_idx') IS NOT NULL`).Scan(&exists); err != nil || exists != step.exists {
			t.Fatalf("index=%v want=%v err=%v", exists, step.exists, err)
		}
	}
}
