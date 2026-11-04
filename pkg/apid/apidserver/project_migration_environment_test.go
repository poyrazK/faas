package apidserver_test

import (
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestProjectApplyPreservesFoldedMigrationCommandAndEnvironment(t *testing.T) {
	spool := t.TempDir()
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", spool)
	t.Setenv("FAAS_SPOOL_ROOT", spool)
	e, _ := apidserver.NewTestServerWithCapturingNotifier(t, api.PlanPro)
	compose := `x-gregale-env-import: true
services:
  migrate:
    image: ghcr.io/example/api:v1
    command: ["./app", "migrate"]
    environment:
      MIGRATIONS_DIR: migrations
      LOG_LEVEL: debug
  api:
    image: ghcr.io/example/api:v1
    ports: ["8080:8080"]
    environment:
      LOG_LEVEL: info
    depends_on:
      migrate:
        condition: service_completed_successfully
`
	applied := applyEnvironmentProject(t, *e.H(), *e.Key(), "", compose)
	if len(applied.Builds) != 1 {
		t.Fatalf("expected only the owner to deploy, got %d deployments", len(applied.Builds))
	}
	deployment, err := (*e.Store()).DeploymentByID(t.Context(), applied.Builds[0].DeploymentID)
	if err != nil || !reflect.DeepEqual(deployment.ReleaseCommand, []string{"./app", "migrate"}) || deployment.ReleaseCommandShell {
		t.Fatalf("folded migration was not retained: command=%v shell=%v err=%v", deployment.ReleaseCommand, deployment.ReleaseCommandShell, err)
	}
	rows, err := (*e.Store()).ListAppEnvInScope(t.Context(), e.Acct().ID, deployment.AppID, api.DefaultEnvScope)
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{}
	for _, row := range rows {
		values[row.Key] = row.Value
	}
	if values["MIGRATIONS_DIR"] != "migrations" || values["LOG_LEVEL"] != "info" {
		t.Fatal("migration defaults or owner environment precedence were lost at apply")
	}
}
