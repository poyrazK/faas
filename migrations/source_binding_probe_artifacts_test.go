//go:build !no_pg

package migrations_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestSourceBindingProbeArtifactMigrationRoundTrip(t *testing.T) {
	pool := openMigrated(t)
	ctx := t.Context()
	account := seedAccount(t, ctx, pool)
	app := seedApp(t, ctx, pool, account)
	dep := seedDeployment(t, ctx, pool, app, "live")
	target := uuid.NewString()
	revision := strings.Repeat("a", 64)
	spec := api.ManagedServiceProbeSpec{Version: 1, CallerDeploymentID: dep, Scope: "default", Service: "billing", Binding: "BILLING_URL", TargetAppID: uuid.NewString(), TargetDeploymentID: target, ReleaseID: uuid.NewString(), ContextRevision: revision}
	pin, err := json.Marshal(map[string]any{"type": "service", "binding": "billing", "revision": revision, "target_deployment_id": target, "managed_service": spec})
	if err != nil {
		t.Fatal(err)
	}
	insert := func(image string) error {
		_, err := pool.Exec(ctx, `INSERT INTO app_tasks(id,account_id,app_id,deployment_id,kind,command,command_shell,deployment_scope,artifact_key,image_digest,timeout_seconds,max_output_bytes,binding_verification) VALUES($1,$2,$3,$4,'manual',$5,false,'default','reviewed/source-rootfs',$6,15,4096,$7::jsonb)`, uuid.NewString(), account, app, dep, []string{api.AppTaskServiceBindingTargetProbeCommand, "billing", target}, image, pin)
		return err
	}
	if err := insert(""); err != nil {
		t.Fatalf("source pin: %v", err)
	}
	source, err := os.ReadFile("20261017180000001_source_binding_probe_artifacts.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, ok := strings.Cut(string(source), "-- +goose Down")
	if !ok {
		t.Fatal("missing downgrade")
	}
	if _, err := pool.Exec(ctx, down); err == nil {
		t.Fatal("downgrade discarded empty-digest source history")
	}
	var retained int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM app_tasks WHERE app_id=$1 AND image_digest=''", app).Scan(&retained); err != nil || retained != 1 {
		t.Fatalf("source history=%d %v", retained, err)
	}
	if _, err := pool.Exec(ctx, "DELETE FROM app_tasks WHERE app_id=$1", app); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, down); err != nil {
		t.Fatal(err)
	}
	if err := insert(""); err == nil {
		t.Fatal("old schema admitted source pin")
	}
	if err := insert("sha256:existing-image"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, up); err != nil {
		t.Fatal(err)
	}
	if err := insert(""); err != nil {
		t.Fatal(err)
	}
}
