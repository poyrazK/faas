//go:build !no_pg

// adr: 426 — rollback retains task history and the older binding families.
package migrations_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestObjectStorageBindingVerificationMigrationRoundTrip(t *testing.T) {
	pool := pgtest.Open(t)
	defer pool.Close()
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	account := seedAccount(t, ctx, pool)
	app := seedApp(t, ctx, pool, account)
	dep := seedDeployment(t, ctx, pool, app, "live")
	objectID := ""
	for _, binding := range []struct{ kind, command string }{
		{api.BindingTypeService, api.AppTaskServiceBindingProbeCommand},
		{api.BindingTypePostgres, api.AppTaskPostgresBindingProbeCommand},
		{api.BindingTypeObjectStorage, api.AppTaskObjectStorageBindingProbeCommand},
	} {
		id := uuid.NewString()
		pin, _ := json.Marshal(map[string]string{"type": binding.kind, "binding": "ASSETS", "revision": strings.Repeat("a", 64)})
		_, err := pool.Exec(ctx, `INSERT INTO app_tasks(id,account_id,app_id,deployment_id,kind,command,command_shell,deployment_scope,artifact_key,image_digest,timeout_seconds,max_output_bytes,binding_verification)
		 VALUES($1,$2,$3,$4,'manual',$5,false,'default','test/rootfs','sha256:test',15,4096,$6::jsonb)`, id, account, app, dep, []string{binding.command, "ASSETS"}, string(pin))
		if err != nil {
			t.Fatalf("admit %s pin: %v", binding.kind, err)
		}
		if binding.kind == api.BindingTypeObjectStorage {
			objectID = id
		}
	}
	_, err := pool.Exec(ctx, `UPDATE app_tasks SET command=ARRAY[$1,'ASSETS'] WHERE id=$2`, api.AppTaskPostgresBindingProbeCommand, objectID)
	var violation *pgconn.PgError
	if !errors.As(err, &violation) || violation.Code != "23514" {
		t.Fatalf("wrong command accepted: %v", err)
	}
	source, err := os.ReadFile("20261002073437738_object_storage_binding_verification.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, ok := strings.Cut(string(source), "-- +goose Down")
	if !ok {
		t.Fatal("rollback missing")
	}
	if _, err := pool.Exec(ctx, down); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	var retained, olderPins int
	if err := pool.QueryRow(ctx, `SELECT count(*),count(binding_verification) FROM app_tasks WHERE app_id=$1`, app).Scan(&retained, &olderPins); err != nil || retained != 3 || olderPins != 2 {
		t.Fatalf("history=%d older pins=%d err=%v", retained, olderPins, err)
	}
	objectPin, _ := json.Marshal(map[string]string{"type": api.BindingTypeObjectStorage, "binding": "ASSETS", "revision": strings.Repeat("a", 64)})
	_, err = pool.Exec(ctx, `UPDATE app_tasks SET binding_verification=$1::jsonb WHERE id=$2`, string(objectPin), objectID)
	if !errors.As(err, &violation) || violation.Code != "23514" {
		t.Fatalf("rollback retained object-storage pin support: %v", err)
	}
	if _, err := pool.Exec(ctx, up); err != nil {
		t.Fatalf("reapply: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE app_tasks SET binding_verification=$1::jsonb WHERE id=$2`, string(objectPin), objectID); err != nil {
		t.Fatalf("reapplied object-storage pin rejected: %v", err)
	}
}
