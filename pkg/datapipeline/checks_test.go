package datapipeline

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestCheckCapacityExpiresBeforeCompatibilityMetadata(t *testing.T) {
	s, store, _, account := testService(t)
	ctx := t.Context()
	c, err := s.CreateConnection(ctx, account, api.CreateDataConnectionRequest{Name: "source", ConnectionURL: "postgres://r:p@db.example/db?sslmode=verify-full"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.RequestCheck(ctx, account, c.ID, c.Revision, ""); err != nil {
		t.Fatal(err)
	}
	job, err := store.ClaimCheck(ctx, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	endpoint := &api.DataEndpointCapacity{State: "ready", ObservedAt: now.Add(-31 * time.Second).Format(time.RFC3339Nano), ExpiresAt: now.Add(-time.Second).Format(time.RFC3339Nano)}
	if err = store.FinishCheck(ctx, job, api.DataConnectionCheck{State: "passed", Tables: []api.DataSourceTable{{Name: "orders", Supported: true}}, Capacity: &api.DataPipelineCapacity{State: "ready", Source: endpoint, Destination: endpoint}, Checks: []api.DataConnectionIssue{capacityIssue("source", endpoint)}}); err != nil {
		t.Fatal(err)
	}
	check, err := store.Check(ctx, account, c.ID)
	if err != nil || !check.Fresh || check.State != "passed" || check.Capacity.State != "needs_qualification" || check.Checks[0].State != "unknown" {
		t.Fatalf("expired capacity survived fresh catalog metadata: %+v %v", check, err)
	}
}

func TestConnectionChecksFenceRotationAndRecoverExpiredLeases(t *testing.T) {
	s, store, _, account := testService(t)
	ctx := context.Background()
	c, err := s.CreateConnection(ctx, account, api.CreateDataConnectionRequest{Name: "source", ConnectionURL: "postgres://r:private-password@db.example/db?sslmode=verify-full"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.RequestCheck(ctx, uuid.NewString(), c.ID, c.Revision, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign check: %v", err)
	}
	if _, err = store.RequestCheck(ctx, account, c.ID, 0, ""); !errors.Is(err, ErrInvalid) {
		t.Fatal("missing revision accepted")
	}
	requested, err := store.RequestCheck(ctx, account, c.ID, c.Revision, "")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			duplicate, err := store.RequestCheck(ctx, account, c.ID, c.Revision, "")
			if err != nil || duplicate.ID != requested.ID {
				t.Errorf("concurrent check replaced an active job: %v", err)
			}
		}()
	}
	wg.Wait()
	job, err := store.ClaimCheck(ctx, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ClaimCheck(ctx, uuid.NewString()); !errors.Is(err, ErrNotFound) {
		t.Fatal("active check claimed twice")
	}
	if _, err = store.pool.Exec(ctx, `UPDATE data_connection_checks SET lease_until=clock_timestamp()-interval '1 second' WHERE connection_id=$1`, c.ID); err != nil {
		t.Fatal(err)
	}
	recovered, err := store.ClaimCheck(ctx, uuid.NewString())
	if err != nil || recovered.ID != job.ID || recovered.LeaseToken == job.LeaseToken {
		t.Fatal("worker restart did not recover the check")
	}
	result := api.DataConnectionCheck{State: "passed", Checks: []api.DataConnectionIssue{}, Tables: []api.DataSourceTable{{Name: "orders", Supported: true}}}
	if err = store.FinishCheck(ctx, job, result); !errors.Is(err, ErrConflict) {
		t.Fatal("expired worker published a check")
	}
	if _, err = s.RotateConnection(ctx, account, c.ID, api.RotateDataConnectionRequest{ExpectedRevision: c.Revision, ConnectionURL: "postgres://r:replacement@db.example/db?sslmode=verify-full"}); err != nil {
		t.Fatal(err)
	}
	if err = store.FinishCheck(ctx, recovered, result); !errors.Is(err, ErrConflict) {
		t.Fatal("old credential produced fresh evidence")
	}
	stale, err := store.Check(ctx, account, c.ID)
	if err != nil || stale.State != "stale" || stale.Fresh {
		t.Fatal("rotated credential retained a usable check")
	}
	newJob, err := store.RequestCheck(ctx, account, c.ID, 2, "")
	if err != nil || newJob.ID == requested.ID || len(newJob.Tables) != 0 {
		t.Fatal("rotation did not start a clean metadata generation")
	}
	if _, err = store.Check(ctx, uuid.NewString(), c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign metadata read succeeded")
	}
	if err = store.DeleteConnection(ctx, account, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Check(ctx, account, c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("source deletion retained its check")
	}
}

func TestPipelineCreationRequiresFreshSupportedSelection(t *testing.T) {
	s, store, _, account := testService(t)
	ctx := context.Background()
	c, err := s.CreateConnection(ctx, account, api.CreateDataConnectionRequest{Name: "source", ConnectionURL: "postgres://r:p@db.example/db?sslmode=verify-full"})
	if err != nil {
		t.Fatal(err)
	}
	r := api.CreateDataPipelineRequest{Name: "orders", SourceConnectionID: c.ID, DatabaseID: uuid.NewString(), Tables: []string{"orders"}, ReadSubjects: []string{"alice"}}
	if _, err = s.Create(ctx, account, r); !errors.Is(err, ErrCheckRequired) {
		t.Fatal("unchecked source created a pipeline")
	}
	fixturePassingCheck(t, store, account, c)
	r.Tables = []string{"undiscovered"}
	if _, err = s.Create(ctx, account, r); !errors.Is(err, ErrCheckRequired) {
		t.Fatal("undiscovered table admitted")
	}
	r.Tables = []string{"orders"}
	if _, err = store.pool.Exec(ctx, `UPDATE data_connection_checks SET observed_at=clock_timestamp()-interval '91 seconds' WHERE connection_id=$1`, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Create(ctx, account, r); !errors.Is(err, ErrCheckRequired) {
		t.Fatal("expired check admitted")
	}
	if _, err = store.pool.Exec(ctx, `UPDATE data_connection_checks SET observed_at=clock_timestamp(),database_id=$2 WHERE connection_id=$1`, c.ID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Create(ctx, account, r); !errors.Is(err, ErrCheckRequired) {
		t.Fatal("check for another mirror admitted")
	}
	if _, err = store.pool.Exec(ctx, `UPDATE data_connection_checks SET database_id=$2 WHERE connection_id=$1`, c.ID, r.DatabaseID); err != nil {
		t.Fatal(err)
	}
	p, err := s.Create(ctx, account, r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.pool.Exec(ctx, `UPDATE data_connection_checks SET observed_at=clock_timestamp()-interval '91 seconds' WHERE connection_id=$1`, c.ID); err != nil {
		t.Fatal(err)
	}
	retry, err := s.Create(ctx, account, r)
	if err != nil || retry.ID != p.ID {
		t.Fatal("expired check prevented recovery of already accepted intent")
	}
}

func TestCheckWorkerHonorsAdmissionAndRedactsSourceErrors(t *testing.T) {
	s, store, key, account := testService(t)
	ctx := context.Background()
	c, err := s.CreateConnection(ctx, account, api.CreateDataConnectionRequest{Name: "source", ConnectionURL: "postgres://r:credential-sentinel@db.example/db?sslmode=verify-full"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.RequestCheck(ctx, account, c.ID, api.RequestDataConnectionCheck{ExpectedRevision: c.Revision}); err != nil {
		t.Fatal(err)
	}
	opens := 0
	engine := &Engine{openSource: func(context.Context, string) (*pgxpool.Pool, error) {
		opens++
		return nil, errors.New("credential-sentinel db.example failed")
	}}
	w := &Worker{Store: store, Identities: []*age.X25519Identity{key}, Allowed: func(context.Context, string) bool { return false }, Engine: engine}
	if err = w.CheckTick(ctx); err != nil {
		t.Fatal(err)
	}
	check, err := store.Check(ctx, account, c.ID)
	if err != nil || check.State != "blocked" || opens != 0 {
		t.Fatal("closed admission probed source")
	}
	// Change the optional destination to request a new job without waiting for
	// the normal request-coalescing interval.
	if _, err = store.RequestCheck(ctx, account, c.ID, c.Revision, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	w.Allowed = s.Allowed
	if err = w.CheckTick(ctx); err != nil {
		t.Fatal(err)
	}
	check, err = store.Check(ctx, account, c.ID)
	if err != nil || check.State != "blocked" || opens != 1 {
		t.Fatal("failed source was not recorded")
	}
	blob, _ := json.Marshal(check.DataConnectionCheck)
	if strings.Contains(string(blob), "credential-sentinel") || strings.Contains(string(blob), "db.example") {
		t.Fatal("private source error escaped worker boundary")
	}
}
