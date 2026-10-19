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
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/connectioncapacity"
	"github.com/onebox-faas/faas/pkg/datapipeline"
	"github.com/onebox-faas/faas/pkg/state"
)

func capacityPipelineService(t *testing.T, pool *pgxpool.Pool) *datapipeline.Service {
	t.Helper()
	key, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	return &datapipeline.Service{CapacityStatus: func(ctx context.Context, account, id string) (*api.DataPipelineCapacity, error) {
		return datapipeline.PipelineCapacityStatus(ctx, connectioncapacity.NewStore(pool), account, id)
	}, Store: datapipeline.NewPostgresStore(pool), Recipient: key.Recipient(), Allowed: func(context.Context, string) bool { return true }, ValidateTarget: func(context.Context, string, string) error { return nil }}
}

func passingCapacityCheck(t *testing.T, service *datapipeline.Service, account string, c api.DataConnection) {
	t.Helper()
	ctx := t.Context()
	if _, err := service.Store.RequestCheck(ctx, account, c.ID, c.Revision, ""); err != nil {
		t.Fatal(err)
	}
	job, err := service.Store.ClaimCheck(ctx, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err = service.Store.FinishCheck(ctx, job, api.DataConnectionCheck{State: "passed", Tables: []api.DataSourceTable{{Name: "orders", Supported: true}}}); err != nil {
		t.Fatal(err)
	}
}

func capacityEvidence(key string, limits connectioncapacity.Limits) connectioncapacity.Evidence {
	now := time.Now().UTC()
	return connectioncapacity.Evidence{Key: connectioncapacity.Fingerprint(key), Placement: "fixture-physical-resource", Generation: 1, ReportSHA256: strings.Repeat("a", 64), Limits: limits, ObservedAt: now, ExpiresAt: now.Add(api.DataPipelineCapacityMaxAge)}
}

func qualifyCapacity(t *testing.T, pool *pgxpool.Pool, account string, c api.DataConnection, d Database, sourceKey string, sourceLimits, targetLimits connectioncapacity.Limits) {
	t.Helper()
	if d.DataResourceID == "" {
		d.DataResourceID = "fixture-data-" + d.ID
		if _, err := pool.Exec(t.Context(), `UPDATE managed_postgres_databases SET data_resource_id=$2 WHERE id=$1 AND data_resource_id IS NULL`, d.ID, d.DataResourceID); err != nil {
			t.Fatal(err)
		}
	}
	store := connectioncapacity.NewStore(pool)
	if _, err := store.RecordSource(t.Context(), account, c.ID, c.Revision, capacityEvidence(sourceKey, sourceLimits)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordTarget(t.Context(), connectioncapacity.Target{AccountID: account, DatabaseID: d.ID, Generation: d.ObservedGeneration, BackendFingerprint: d.BackendFingerprint, DataResourceID: d.DataResourceID}, capacityEvidence(d.ID, targetLimits)); err != nil {
		t.Fatal(err)
	}
}

func TestCapacityAdmissionSerializesSharedSourcesAcrossAccounts(t *testing.T) {
	s, pool, ctx, account := postgresStoreFixture(t)
	service := capacityPipelineService(t, pool)
	requests := []api.CreateDataPipelineRequest{}
	accounts := []string{account}
	other, err := state.NewPgStore(pool).CreateAccount(ctx, uuid.NewString()+"@capacity.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	accounts = append(accounts, other.ID)
	for i, acct := range accounts {
		d := postgresReadyDatabase(t, s, acct, "shared-target", time.Now().UTC())
		host := []string{"db.example", "alias.example"}[i]
		c, err := service.CreateConnection(ctx, acct, api.CreateDataConnectionRequest{Name: "source", ConnectionURL: "postgres://r:p@" + host + "/db?sslmode=verify-full"})
		if err != nil {
			t.Fatal(err)
		}
		passingCapacityCheck(t, service, acct, c)
		qualifyCapacity(t, pool, acct, c, d, "shared-cluster", connectioncapacity.Limits{Connections: 10, Slots: 3, Senders: 3}, connectioncapacity.Limits{Connections: 100, Workers: 3})
		requests = append(requests, api.CreateDataPipelineRequest{Name: "orders", SourceConnectionID: c.ID, DatabaseID: d.ID, Tables: []string{"orders"}, ReadSubjects: []string{"alice"}})
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i := range requests {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := service.Create(ctx, accounts[i], requests[i])
			results <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if !errors.Is(err, connectioncapacity.ErrInsufficient) {
			t.Fatalf("unexpected refusal: %v", err)
		}
	}
	if success != 1 {
		t.Fatalf("%d pipelines claimed one physical slot allocation", success)
	}
	var count, slots int64
	if err := pool.QueryRow(ctx, `SELECT count(*),coalesce(sum(slots),0)::bigint FROM postgres_capacity_reservations`).Scan(&count, &slots); err != nil || count != 2 || slots != 3 {
		t.Fatalf("partial or duplicate reservations: %d %d %v", count, slots, err)
	}
}

func TestCapacityAdmissionExpiryRollbackAndDurableCleanup(t *testing.T) {
	s, pool, ctx, account := postgresStoreFixture(t)
	service := capacityPipelineService(t, pool)
	d := postgresReadyDatabase(t, s, account, "capacity-target", time.Now().UTC())
	c, err := service.CreateConnection(ctx, account, api.CreateDataConnectionRequest{Name: "source", ConnectionURL: "postgres://r:p@db.example/db?sslmode=verify-full"})
	if err != nil {
		t.Fatal(err)
	}
	passingCapacityCheck(t, service, account, c)
	req := api.CreateDataPipelineRequest{Name: "orders", SourceConnectionID: c.ID, DatabaseID: d.ID, Tables: []string{"orders"}, ReadSubjects: []string{"alice"}}
	if _, err = service.Create(ctx, account, req); !errors.Is(err, connectioncapacity.ErrUnknown) {
		t.Fatalf("unqualified capacity admitted: %v", err)
	}
	qualifyCapacity(t, pool, account, c, d, "source", connectioncapacity.Limits{Connections: 10, Slots: 3, Senders: 3}, connectioncapacity.Limits{Connections: 4, Workers: 3})
	if _, err = service.Create(ctx, account, req); !errors.Is(err, connectioncapacity.ErrInsufficient) {
		t.Fatalf("undersized target admitted: %v", err)
	}
	assertCapacityHolds(t, pool, 0)
	qualifyCapacity(t, pool, account, c, d, "source", connectioncapacity.Limits{Connections: 10, Slots: 3, Senders: 3}, connectioncapacity.Limits{Connections: 100, Workers: 3})
	if _, err = pool.Exec(ctx, `UPDATE postgres_capacity_resources SET observed_at=clock_timestamp()-interval '32 seconds',expires_at=clock_timestamp()-interval '3 seconds'`); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Create(ctx, account, req); !errors.Is(err, connectioncapacity.ErrUnknown) {
		t.Fatalf("expired evidence admitted: %v", err)
	}
	assertCapacityHolds(t, pool, 0)
	qualifyCapacity(t, pool, account, c, d, "source", connectioncapacity.Limits{Connections: 10, Slots: 3, Senders: 3}, connectioncapacity.Limits{Connections: 100, Workers: 3})
	p, err := service.Create(ctx, account, req)
	if err != nil {
		t.Fatal(err)
	}
	assertCapacityHolds(t, pool, 2)
	described, err := service.Get(ctx, account, p.ID)
	if err != nil || described.Capacity == nil || described.Capacity.State != "ready" || described.Capacity.Source.RequiredConnections != 0 {
		t.Fatal("reserved capacity was counted twice", described, err)
	}
	if _, err = service.Get(ctx, uuid.NewString(), p.ID); !errors.Is(err, datapipeline.ErrNotFound) {
		t.Fatal("foreign pipeline capacity escaped", err)
	}
	// Lost HTTP acknowledgements recover the exact intent without a second hold.
	if retry, err := service.Create(ctx, account, req); err != nil || retry.ID != p.ID {
		t.Fatal(retry, err)
	}
	assertCapacityHolds(t, pool, 2)
	changed := capacityEvidence("other-scope", connectioncapacity.Limits{Connections: 100, Slots: 20, Senders: 20})
	if _, err = connectioncapacity.NewStore(pool).RecordSource(ctx, account, c.ID, c.Revision, changed); !errors.Is(err, connectioncapacity.ErrUnknown) {
		t.Fatalf("active source moved allocation: %v", err)
	}
	claimed, err := service.Store.Claim(ctx, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	paused, err := service.Store.Control(ctx, account, p.ID, p.Revision, "pause")
	if err != nil {
		t.Fatal(err)
	}
	if err = service.Store.Finish(ctx, claimed, datapipeline.Observation{State: "deleted"}); !errors.Is(err, datapipeline.ErrConflict) {
		t.Fatalf("stale cleanup accepted: %v", err)
	}
	assertCapacityHolds(t, pool, 2)
	if _, err = service.Store.Control(ctx, account, p.ID, paused.Revision, "delete"); err != nil {
		t.Fatal(err)
	}
	assertCapacityHolds(t, pool, 2)
	// A restarted worker confirms remote cleanup under the current intent lease.
	restarted := datapipeline.NewPostgresStore(pool)
	claimed, err = restarted.Claim(ctx, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err = restarted.Finish(ctx, claimed, datapipeline.Observation{State: "deleted"}); err != nil {
		t.Fatal(err)
	}
	assertCapacityHolds(t, pool, 0)
}

func assertCapacityHolds(t *testing.T, pool *pgxpool.Pool, want int64) {
	t.Helper()
	var got int64
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM postgres_capacity_reservations`).Scan(&got); err != nil || got != want {
		t.Fatalf("holds=%d want %d: %v", got, want, err)
	}
}

func TestCapacityBindingReservationsCoverRotationAndConcurrentClients(t *testing.T) {
	s, pool, ctx, account := postgresStoreFixture(t)
	service := capacityPipelineService(t, pool)
	d := postgresReadyDatabase(t, s, account, "binding-capacity", time.Now().UTC())
	c, err := service.CreateConnection(ctx, account, api.CreateDataConnectionRequest{Name: "source", ConnectionURL: "postgres://r:p@db.example/db?sslmode=verify-full"})
	if err != nil {
		t.Fatal(err)
	}
	passingCapacityCheck(t, service, account, c)
	qualifyCapacity(t, pool, account, c, d, "source", connectioncapacity.Limits{Connections: 10, Slots: 3, Senders: 3}, connectioncapacity.Limits{Connections: 91, Workers: 3})
	if _, err = service.Create(ctx, account, api.CreateDataPipelineRequest{Name: "orders", SourceConnectionID: c.ID, DatabaseID: d.ID, Tables: []string{"orders"}, ReadSubjects: []string{"alice"}}); err != nil {
		t.Fatal(err)
	}
	bindings := []Binding{}
	for _, slug := range []string{"first-api", "second-api"} {
		app, err := state.NewPgStore(pool).CreateApp(ctx, state.App{AccountID: account, Slug: slug, Status: state.AppActive})
		if err != nil {
			t.Fatal(err)
		}
		b := testBinding(account, d.ID, app.ID, uuid.NewString(), time.Now().UTC())
		b.Access = CredentialReadOnly
		bindings = append(bindings, b)
	}
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, binding := range bindings {
		wg.Add(1)
		go func(b Binding) {
			defer wg.Done()
			got, _, err := s.ReserveBinding(ctx, b)
			if err == nil && got.ConnectionLimit != api.DataPipelineBindingConnections {
				err = errors.New("missing enforced login ceiling")
			}
			results <- err
		}(binding)
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if !errors.Is(err, connectioncapacity.ErrInsufficient) {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatalf("admitted %d bindings", success)
	}
	_, snap, err := connectioncapacity.NewStore(pool).Target(ctx, account, d.ID)
	if err != nil || snap.Reserved.Connections != connectioncapacity.PipelineTargetDemand().Connections+2*api.DataPipelineBindingConnections+api.DataPipelineBindingControlConnections {
		t.Fatal(snap, err)
	}
	budget := &BindingService{databases: s, bindings: s, now: time.Now}
	got, err := budget.ConnectionBudget(ctx, account, d.ID)
	if err != nil || !got.Enforced || got.State != "within_budget" || got.Demand.ModeledPeakConnections != 91 {
		t.Fatal(got, err)
	}
	if _, _, err = connectioncapacity.NewStore(pool).Target(ctx, uuid.NewString(), d.ID); err == nil {
		t.Fatal("cross-account capacity leaked")
	}
}

func TestCapacityBindingCannotDowngradeAfterPlacementChanges(t *testing.T) {
	s, pool, ctx, account := postgresStoreFixture(t)
	service := capacityPipelineService(t, pool)
	d := postgresReadyDatabase(t, s, account, "capacity-fence", time.Now().UTC())
	c, err := service.CreateConnection(ctx, account, api.CreateDataConnectionRequest{Name: "source", ConnectionURL: "postgres://r:p@db.example/db?sslmode=verify-full"})
	if err != nil {
		t.Fatal(err)
	}
	qualifyCapacity(t, pool, account, c, d, "source", connectioncapacity.Limits{Connections: 10, Slots: 3, Senders: 3}, connectioncapacity.Limits{Connections: 100, Workers: 3})
	// Model an expired placement mapping after a database generation change.
	if _, err = pool.Exec(ctx, `UPDATE postgres_capacity_targets SET database_generation=database_generation+1 WHERE database_id=$1`, d.ID); err != nil {
		t.Fatal(err)
	}
	app, err := state.NewPgStore(pool).CreateApp(ctx, state.App{AccountID: account, Slug: "fenced-api", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	b := testBinding(account, d.ID, app.ID, uuid.NewString(), time.Now().UTC())
	b.Access = CredentialReadOnly
	if _, _, err = s.ReserveBinding(ctx, b); !errors.Is(err, connectioncapacity.ErrUnknown) {
		t.Fatalf("stale qualification issued an unbounded login: %v", err)
	}
	assertCapacityHolds(t, pool, 0)
}
