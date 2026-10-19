package datapipeline

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func testService(t *testing.T) (*Service, *PostgresStore, *age.X25519Identity, string) {
	t.Helper()
	pool := pgtest.Open(t)

	schema, err := os.ReadFile("schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(context.Background(), string(schema)); err != nil {
		t.Fatal(err)
	}
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	store := NewPostgresStore(pool)
	return &Service{Store: store, Recipient: identity.Recipient(), Allowed: func(context.Context, string) bool { return true }, ValidateTarget: func(context.Context, string, string) error { return nil }}, store, identity, uuid.NewString()
}

func TestConnectionCustodyRotationAndTenantIsolation(t *testing.T) {
	s, store, key, account := testService(t)
	ctx := context.Background()
	for _, raw := range []string{"postgres://reader@db.example/orders?sslmode=verify-full", "postgres://reader:@db.example/orders?sslmode=verify-full"} {
		if _, e := s.CreateConnection(ctx, account, api.CreateDataConnectionRequest{Name: "implicit-password", ConnectionURL: raw}); !errors.Is(e, ErrInvalid) {
			t.Fatal("source accepted an inherited password")
		}
	}
	raw := "postgres://reader:sentinel-secret@db.example/orders?sslmode=verify-full"
	c, err := s.CreateConnection(ctx, account, api.CreateDataConnectionRequest{Name: "orders", ConnectionURL: raw})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := store.Connection(ctx, account, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(saved.Ciphertext), "sentinel-secret") {
		t.Fatal("plaintext credential persisted")
	}
	opened, err := openConnection([]*age.X25519Identity{key}, saved)
	if err != nil || opened != raw {
		t.Fatal("sealed credential did not recover")
	}
	wrong := saved
	wrong.AccountID = uuid.NewString()
	if _, err = openConnection([]*age.X25519Identity{key}, wrong); err == nil {
		t.Fatal("credential opened for another account")
	}
	if _, err = store.Connection(ctx, uuid.NewString(), c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign account lookup: %v", err)
	}
	if _, err = s.RotateConnection(ctx, account, c.ID, api.RotateDataConnectionRequest{ExpectedRevision: c.Revision, ConnectionURL: "postgres://reader:new@other.example/orders?sslmode=verify-full"}); !errors.Is(err, ErrConflict) {
		t.Fatal("rotation changed database identity")
	}
	rotated, err := s.RotateConnection(ctx, account, c.ID, api.RotateDataConnectionRequest{ExpectedRevision: c.Revision, ConnectionURL: strings.Replace(raw, "sentinel-secret", "replacement", 1)})
	if err != nil || rotated.Revision != 2 {
		t.Fatalf("rotation failed: %v", err)
	}
	if _, err = s.RotateConnection(ctx, account, c.ID, api.RotateDataConnectionRequest{ExpectedRevision: 1, ConnectionURL: raw}); !errors.Is(err, ErrConflict) {
		t.Fatal("stale credential revision accepted")
	}
}

func TestPipelineAdmissionAndStaleWorkerRecovery(t *testing.T) {
	s, store, _, account := testService(t)
	ctx := context.Background()
	c, err := s.CreateConnection(ctx, account, api.CreateDataConnectionRequest{Name: "source", ConnectionURL: "postgres://reader:secret@db.example/db?sslmode=verify-full"})
	if err != nil {
		t.Fatal(err)
	}
	fixturePassingCheck(t, store, account, c)
	req := api.CreateDataPipelineRequest{Name: "orders", SourceConnectionID: c.ID, DatabaseID: uuid.NewString(), Tables: []string{"orders"}, ReadSubjects: []string{"alice"}}
	p, err := s.Create(ctx, account, req)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := s.Create(ctx, account, req)
	if err != nil || retry.ID != p.ID {
		t.Fatal("same intent did not recover")
	}
	foreign := req
	foreign.Name = "foreign"
	if _, err = s.Create(ctx, uuid.NewString(), foreign); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign source admitted: %v", err)
	}
	if err = store.DeleteConnection(ctx, account, c.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("deleted active source: %v", err)
	}
	claimed, err := store.Claim(ctx, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	paused, err := store.Control(ctx, account, p.ID, p.Revision, "pause")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Finish(ctx, claimed, Observation{State: "live", Verification: "unknown"}); !errors.Is(err, ErrConflict) {
		t.Fatal("stale worker replaced newer intent")
	}
	current, err := store.Get(ctx, account, p.ID)
	if err != nil || current.DesiredState != "paused" || current.Revision != paused.Revision {
		t.Fatal("pause intent lost")
	}
	// Simulate worker death and an expired lease. A fresh owner must recover.
	if _, err = store.pool.Exec(ctx, `UPDATE data_pipelines SET lease_token='retired',lease_until=clock_timestamp()-interval '1 second',retry_at=clock_timestamp() WHERE id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	recovered, err := store.Claim(ctx, uuid.NewString())
	if err != nil || recovered.LeaseToken == "retired" {
		t.Fatal("expired lease not recovered")
	}
	if err = store.Finish(ctx, recovered, Observation{State: "paused", Verification: "unknown"}); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentConnectionQuota(t *testing.T) {
	s, _, _, account := testService(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	var mu sync.Mutex
	success := 0
	for n := 0; n < api.DataPipelineConnectionsMax+5; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.CreateConnection(ctx, account, api.CreateDataConnectionRequest{Name: "source-" + uuid.NewString()[:18], ConnectionURL: "postgres://r:p@db.example/db?sslmode=verify-full"})
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				success++
			} else if !errors.Is(err, ErrQuota) {
				t.Errorf("unexpected reservation error: %v", err)
			}
		}()
	}
	wg.Wait()
	if success != api.DataPipelineConnectionsMax {
		t.Fatalf("quota race admitted %d sources", success)
	}
}

func TestPipelineSelectionValidation(t *testing.T) {
	valid := api.CreateDataPipelineRequest{Name: "orders", SourceConnectionID: uuid.NewString(), DatabaseID: uuid.NewString(), Tables: []string{"orders"}, ReadSubjects: []string{"alice"}}
	if err := ValidateCreate(valid); err != nil {
		t.Fatal(err)
	}
	for _, tables := range [][]string{{"orders;drop table accounts"}, {"orders", "orders"}, {"public.orders"}, {"gregale_outbox"}, nil} {
		bad := valid
		bad.Tables = tables
		if ValidateCreate(bad) == nil {
			t.Fatalf("unsafe selection accepted: %q", tables)
		}
	}
	bad := valid
	bad.ReadSubjects = nil
	if ValidateCreate(bad) == nil {
		t.Fatal("default public read access accepted")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	s := Service{Allowed: func(context.Context, string) bool { return false }}
	if _, err := s.Create(ctx, uuid.NewString(), valid); !errors.Is(err, ErrUnavailable) {
		t.Fatal("closed admission bypassed")
	}
}

func TestVerificationRetryAndObservationFreshness(t *testing.T) {
	s, store, _, account := testService(t)
	ctx := context.Background()
	c, err := s.CreateConnection(ctx, account, api.CreateDataConnectionRequest{Name: "source", ConnectionURL: "postgres://r:p@db.example/db?sslmode=verify-full"})
	if err != nil {
		t.Fatal(err)
	}
	fixturePassingCheck(t, store, account, c)
	p, err := s.Create(ctx, account, api.CreateDataPipelineRequest{Name: "orders", SourceConnectionID: c.ID, DatabaseID: uuid.NewString(), Tables: []string{"orders"}, ReadSubjects: []string{"alice"}})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := store.Claim(ctx, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Finish(ctx, claim, Observation{State: "live", Verification: "unknown", Initialized: true}); err != nil {
		t.Fatal(err)
	}
	current, err := store.Get(ctx, account, p.ID)
	if err != nil || !current.ObservationFresh || !current.Initialized {
		t.Fatalf("fresh successful observation missing: %+v %v", current, err)
	}
	requested, err := store.Control(ctx, account, p.ID, p.Revision, "verify")
	if err != nil {
		t.Fatal(err)
	}
	if requested.ObservationFresh || requested.Verification != "pending" {
		t.Fatal("new intent incorrectly observed")
	}
	claim, err = store.Claim(ctx, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Finish(ctx, claim, Observation{State: "blocked", Reason: "database_unavailable", Verification: claim.Verification}); err != nil {
		t.Fatal(err)
	}
	current, err = store.Get(ctx, account, p.ID)
	if err != nil || !current.VerifyRequested || !current.Initialized {
		t.Fatal("transient failure consumed verification or initialization receipt")
	}
	if _, err = store.pool.Exec(ctx, `UPDATE data_pipelines SET observed_at=clock_timestamp()-interval '91 seconds' WHERE id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	current, err = store.Get(ctx, account, p.ID)
	if err != nil || current.ObservationFresh {
		t.Fatal("stale health observation reported fresh")
	}
}

func TestPipelineDeleteRetryRecoversExistingIntent(t *testing.T) {
	s, store, _, account := testService(t)
	ctx := context.Background()
	c, err := s.CreateConnection(ctx, account, api.CreateDataConnectionRequest{Name: "source", ConnectionURL: "postgres://r:p@db.example/db?sslmode=verify-full"})
	if err != nil {
		t.Fatal(err)
	}
	fixturePassingCheck(t, store, account, c)
	p, err := s.Create(ctx, account, api.CreateDataPipelineRequest{Name: "orders", SourceConnectionID: c.ID, DatabaseID: uuid.NewString(), Tables: []string{"orders"}, ReadSubjects: []string{"alice"}})
	if err != nil {
		t.Fatal(err)
	}
	requested, err := store.Control(ctx, account, p.ID, p.Revision, "delete")
	if err != nil {
		t.Fatal(err)
	}
	retry, err := store.Control(ctx, account, p.ID, requested.Revision, "delete")
	if err != nil || retry.Revision != requested.Revision || retry.DesiredState != "deleted" {
		t.Fatal("lost delete acknowledgement could not recover", err)
	}
	if _, err = store.Control(ctx, account, p.ID, p.Revision, "delete"); !errors.Is(err, ErrConflict) {
		t.Fatal("stale delete fence bypassed")
	}
}

// Lifecycle tests start with a worker-approved catalog fixture. Native acceptance
// exercises the actual metadata inspector before creating the pipeline.
func fixturePassingCheck(t *testing.T, store *PostgresStore, account string, c api.DataConnection) {
	t.Helper()
	ctx := context.Background()
	if _, err := store.RequestCheck(ctx, account, c.ID, c.Revision, ""); err != nil {
		t.Fatal(err)
	}
	job, err := store.ClaimCheck(ctx, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err = store.FinishCheck(ctx, job, api.DataConnectionCheck{State: "passed", Checks: []api.DataConnectionIssue{}, Tables: []api.DataSourceTable{{Name: "orders", Supported: true, Columns: []api.DataSourceColumn{}, Issues: []api.DataConnectionIssue{}}}}); err != nil {
		t.Fatal(err)
	}
}
