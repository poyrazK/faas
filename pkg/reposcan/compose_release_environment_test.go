package reposcan_test

import (
	"testing"
	"testing/fstest"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/projectsetup"
	"github.com/onebox-faas/faas/pkg/reposcan"
)

func TestFoldedMigrationKeepsEnvironmentDefaultsAndDatabaseKeys(t *testing.T) {
	fsys := fstest.MapFS{"compose.yaml": {Data: []byte(`x-gregale-env-import: true
services:
  migrate:
    image: ghcr.io/example/api:v1
    command: ["./app", "migrate"]
    environment:
      MIGRATIONS_DIR: migrations
      DB_CONNECTION: postgres://app:password@db:5432/shop
      LOG_LEVEL: debug
      API_TOKEN: never-import-this-value
    depends_on: [db]
  api:
    image: ghcr.io/example/api:v1
    ports: ["8080:8080"]
    environment:
      LOG_LEVEL: info
    depends_on:
      migrate:
        condition: service_completed_successfully
  db:
    image: postgres:16
`)}}
	scan, err := reposcan.Scan(fsys)
	if err != nil || len(scan.Workloads) != 1 {
		t.Fatalf("scan: workloads=%d err=%v", len(scan.Workloads), err)
	}
	env, err := reposcan.ReadProjectEnv(fsys, reposcan.ScanOptions{}, scan.Workloads)
	if err != nil {
		t.Fatal(err)
	}
	plan := projectsetup.PlanEnvImport(env, "api", []string{"api"}, api.MustLimitsFor(api.PlanPro), false)
	if plan.Values["MIGRATIONS_DIR"] != "migrations" || plan.Values["LOG_LEVEL"] != "info" {
		t.Fatal("migration defaults were lost or replaced the owner's explicit declaration")
	}
	if _, imported := plan.Values["API_TOKEN"]; imported || plan.Skipped["API_TOKEN"] != projectsetup.EnvSkipSecret {
		t.Fatal("folding a migration bypassed secret import restrictions")
	}
	consumers := projectsetup.ManagedConsumers(env, scan.Workloads, "db", "DATABASE_URL")
	if len(consumers) != 1 || consumers[0].Workload != "api" || consumers[0].EnvKey != "DB_CONNECTION" {
		t.Fatalf("migration's database key was replaced by a guessed key: %+v", consumers)
	}
}
