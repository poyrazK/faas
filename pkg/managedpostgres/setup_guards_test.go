package managedpostgres

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/datapipeline"
)

func TestSetupMigrationProtectsAccountSourceAndMirrorUntilStopped(t *testing.T) {
	store, pool, ctx, account := postgresStoreFixture(t)
	database := postgresReadyDatabase(t, store, account, "setup-mirror", time.Now().UTC())
	service := capacityPipelineService(t, pool)
	c, err := service.CreateConnection(ctx, account, api.CreateDataConnectionRequest{Name: "setup-source", ConnectionURL: "postgres://r:p@db.example/db?sslmode=verify-full"})
	if err != nil {
		t.Fatal(err)
	}
	setupStore := service.Store.(datapipeline.SetupStore)
	job, err := setupStore.EnqueueSetup(ctx, datapipeline.Setup{ID: uuid.NewString(), AccountID: account, DatabaseID: database.ID, MirrorName: database.Name, Request: datapipeline.SetupRequest{Name: "guarded-setup", SourceConnectionID: c.ID, DatabaseID: database.ID, Tables: []string{"orders"}, ReadSubjects: []string{"alice"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, probe := range []struct{ query, id string }{
		{`DELETE FROM accounts WHERE id=$1`, account},
		{`DELETE FROM data_pipeline_connections WHERE id=$1`, c.ID},
		{`DELETE FROM managed_postgres_databases WHERE id=$1`, database.ID},
		{`UPDATE managed_postgres_databases SET state='deleting' WHERE id=$1`, database.ID},
		{`UPDATE managed_postgres_databases SET desired_generation=desired_generation+1 WHERE id=$1`, database.ID},
	} {
		if _, err := pool.Exec(ctx, probe.query, probe.id); err == nil {
			t.Fatalf("active setup lifecycle guard missing: %s", probe.query)
		}
	}
	if _, err := setupStore.ControlSetup(ctx, account, job.ID, job.Revision, "cancel"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM data_pipeline_connections WHERE id=$1`, c.ID); err != nil {
		t.Fatalf("stopped setup retained source lock: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE managed_postgres_databases SET state='deleting' WHERE id=$1`, database.ID); err != nil {
		t.Fatalf("stopped setup retained mirror lock: %v", err)
	}
}
