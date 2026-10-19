package managedpostgres

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/connectioncapacity"
	"github.com/onebox-faas/faas/pkg/datapipeline"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPipelineDatabaseGuardsBudgetAndMigrationReplay(t *testing.T) {
	s, pool, ctx, account := postgresStoreFixture(t)
	d := postgresReadyDatabase(t, s, account, "pipeline-target", time.Now().UTC())
	key, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	service := datapipeline.Service{Store: datapipeline.NewPostgresStore(pool), Recipient: key.Recipient(), Allowed: func(context.Context, string) bool { return true }, ValidateTarget: func(context.Context, string, string) error { return nil }}
	source, err := service.CreateConnection(ctx, account, api.CreateDataConnectionRequest{Name: "source", ConnectionURL: "postgres://r:p@db.example/db?sslmode=verify-full"})
	if err != nil {
		t.Fatal(err)
	}
	passingCapacityCheck(t, &service, account, source)
	qualifyCapacity(t, pool, account, source, d, "source", connectioncapacity.Limits{Connections: 10, Slots: 3, Senders: 3}, connectioncapacity.Limits{Connections: 100, Workers: 3})
	p, err := service.Create(ctx, account, api.CreateDataPipelineRequest{Name: "orders", SourceConnectionID: source.ID, DatabaseID: d.ID, Tables: []string{"orders"}, ReadSubjects: []string{"alice"}})
	if err != nil {
		t.Fatal(err)
	}
	app, err := state.NewPgStore(pool).CreateApp(ctx, state.App{AccountID: account, Slug: "pipeline-" + uuid.NewString()[:8], Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	binding := testBinding(account, d.ID, app.ID, uuid.NewString(), time.Now().UTC())
	binding.Access = CredentialDataAPI
	if _, _, err = s.ReserveBinding(ctx, binding); err == nil {
		t.Fatal("writable binding admitted to mirror")
	}
	binding.Access = CredentialReadOnly
	if _, _, err = s.ReserveBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	budget := &BindingService{databases: s, bindings: s, now: time.Now}
	got, err := budget.AppConnectionBudgets(ctx, account, app.ID, app.Slug)
	if err != nil || len(got.Databases) != 1 || got.State != "within_budget" || !got.Databases[0].Enforced {
		t.Fatalf("mirror omitted from budget: %+v %v", got, err)
	}
	for _, q := range []string{`UPDATE managed_postgres_databases SET state='deleting' WHERE id=$1`, `DELETE FROM managed_postgres_databases WHERE id=$1`} {
		if _, err = pool.Exec(ctx, q, d.ID); err == nil {
			t.Fatal("active mirror lifecycle was not reserved")
		}
	}
	if _, err = pool.Exec(ctx, `DELETE FROM accounts WHERE id=$1`, account); err == nil {
		t.Fatal("account deletion bypassed remote cleanup")
	}
	// Replaying the migration must preserve durable customer intent and guards.
	files, err := migrations.FS.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if strings.HasSuffix(file.Name(), "_data_pipelines.sql") || strings.HasSuffix(file.Name(), "_qualified_pipeline_capacity.sql") {
			data, e := migrations.FS.ReadFile(file.Name())
			if e != nil {
				t.Fatal(e)
			}
			up, _, _ := strings.Cut(string(data), "-- +goose Down")
			if _, e = pool.Exec(ctx, up); e != nil {
				t.Fatal(e)
			}
		}
	}
	assertCapacityHolds(t, pool, 3)
	if current, e := service.Store.Get(ctx, account, p.ID); e != nil || current.ID != p.ID {
		t.Fatal("migration replay replaced intent")
	}
	if _, e := pool.Exec(ctx, `UPDATE managed_postgres_databases SET state='deleting' WHERE id=$1`, d.ID); e == nil {
		t.Fatal("replayed guard absent")
	}
	if _, err = service.Store.Control(ctx, account, p.ID, p.Revision, "delete"); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE managed_postgres_databases SET state='deleting' WHERE id=$1`, d.ID); err == nil {
		t.Fatal("delete request released mirror before remote cleanup")
	}
}

func TestPipelineAndWritableBindingReservationSerialize(t *testing.T) {
	s, pool, ctx, account := postgresStoreFixture(t)
	d := postgresReadyDatabase(t, s, account, "pipeline-race", time.Now().UTC())
	key, _ := age.GenerateX25519Identity()
	service := datapipeline.Service{Store: datapipeline.NewPostgresStore(pool), Recipient: key.Recipient(), Allowed: func(context.Context, string) bool { return true }, ValidateTarget: func(context.Context, string, string) error { return nil }}
	c, err := service.CreateConnection(ctx, account, api.CreateDataConnectionRequest{Name: "source", ConnectionURL: "postgres://r:p@db.example/db?sslmode=verify-full"})
	if err != nil {
		t.Fatal(err)
	}
	passingCapacityCheck(t, &service, account, c)
	qualifyCapacity(t, pool, account, c, d, "source", connectioncapacity.Limits{Connections: 10, Slots: 3, Senders: 3}, connectioncapacity.Limits{Connections: 100, Workers: 3})
	app, err := state.NewPgStore(pool).CreateApp(ctx, state.App{AccountID: account, Slug: "pipeline-race-" + uuid.NewString()[:8], Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	binding := testBinding(account, d.ID, app.ID, uuid.NewString(), time.Now().UTC())
	binding.Access = CredentialDataAPI
	start := make(chan struct{})
	result := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		_, e := service.Create(ctx, account, api.CreateDataPipelineRequest{Name: "orders", SourceConnectionID: c.ID, DatabaseID: d.ID, Tables: []string{"orders"}, ReadSubjects: []string{"alice"}})
		result <- e
	}()
	go func() { defer wg.Done(); <-start; _, _, e := s.ReserveBinding(ctx, binding); result <- e }()
	close(start)
	wg.Wait()
	close(result)
	success := 0
	for e := range result {
		if e == nil {
			success++
		} else if errors.Is(e, context.Canceled) {
			t.Fatal(e)
		}
	}
	if success != 1 {
		t.Fatalf("expected one serialized owner, got %d", success)
	}
}
