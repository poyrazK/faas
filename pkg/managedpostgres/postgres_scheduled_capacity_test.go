package managedpostgres

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/connectioncapacity"
)

func TestCapacityScheduledPipelineReservesConnectionsWithoutReplication(t *testing.T) {
	s, pool, ctx, account := postgresStoreFixture(t)
	service := capacityPipelineService(t, pool)
	d := postgresReadyDatabase(t, s, account, "scheduled-target", time.Now().UTC())
	c, err := service.CreateConnection(ctx, account, api.CreateDataConnectionRequest{Name: "source", ConnectionURL: "postgres://r:p@db.example/db?sslmode=verify-full"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.RequestCheck(ctx, account, c.ID, api.RequestDataConnectionCheck{ExpectedRevision: c.Revision, Mode: "snapshot"}); err != nil {
		t.Fatal(err)
	}
	job, err := service.Store.ClaimCheck(ctx, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err = service.Store.FinishCheck(ctx, job, api.DataConnectionCheck{State: "passed", Tables: []api.DataSourceTable{{Name: "orders", Supported: true}}}); err != nil {
		t.Fatal(err)
	}
	r := api.CreateDataPipelineRequest{Name: "scheduled", SourceConnectionID: c.ID, DatabaseID: d.ID, Tables: []string{"orders"}, ReadSubjects: []string{"alice"}, Operations: &api.DataPipelineOperations{Version: 1, Snapshot: &api.DataPipelineSnapshotSchedule{EverySeconds: 3600}}}
	if _, err = service.Create(ctx, account, r); !errors.Is(err, connectioncapacity.ErrUnknown) {
		t.Fatal("unqualified snapshot admitted", err)
	}
	qualifyCapacity(t, pool, account, c, d, "readonly-resource", connectioncapacity.Limits{Connections: 2}, connectioncapacity.Limits{Connections: 2})
	p, err := service.Create(ctx, account, r)
	if err != nil {
		t.Fatal("connection-only snapshot refused", err)
	}
	var count, connections, native int64
	if err = pool.QueryRow(ctx, `SELECT count(*),sum(connections),sum(slots+senders+workers) FROM postgres_capacity_reservations WHERE owner_kind='pipeline' AND owner_id=$1`, p.ID).Scan(&count, &connections, &native); err != nil || count != 2 || connections != 4 || native != 0 {
		t.Fatal("incorrect snapshot peak reservation", count, connections, native, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE data_pipelines SET work_config='{}' WHERE id=$1`, p.ID); err == nil {
		t.Fatal("SQL changed import mode")
	}
	if _, err = service.Create(ctx, account, r); err != nil {
		t.Fatal("idempotent retry consumed capacity twice", err)
	}
}
