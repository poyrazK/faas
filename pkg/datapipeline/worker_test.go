package datapipeline

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestWorkerRunRefreshesFullPilotCatalog(t *testing.T) {
	_, store, key, _ := testService(t)
	// Unopenable credentials and an unavailable destination exercise real claims
	// and observation commits without dialing hundreds of endpoints. Destination
	// safety reconciliation must fail closed rather than acknowledge suspension.
	// Each account stays within quota.
	for _, query := range []struct {
		sql   string
		count int
	}{
		{`INSERT INTO data_pipeline_connections(id,account_id,name,ciphertext,identity)
 SELECT gen_random_uuid(),gen_random_uuid(),'source',decode('00','hex'),repeat('0',64) FROM generate_series(1,$1)`, api.DataPipelinePilotAccountsMax},
		{`INSERT INTO data_pipelines(id,account_id,name,source_connection_id,database_id,tables,read_subjects)
 SELECT gen_random_uuid(),c.account_id,'pipeline-'||n,c.id,gen_random_uuid(),'["orders"]','["alice"]'
 FROM data_pipeline_connections c CROSS JOIN generate_series(1,$1) n`, api.DataPipelinesMax},
	} {
		if _, err := store.pool.Exec(t.Context(), query.sql, query.count); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.pool.Exec(t.Context(), `INSERT INTO data_connection_checks(connection_id,account_id,id,connection_revision)
 SELECT id,account_id,gen_random_uuid(),revision FROM data_pipeline_connections`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	var setups atomic.Int32
	w := &Worker{Store: store, Engine: &Engine{}, Identities: []*age.X25519Identity{key},
		Allowed: func(context.Context, string) bool { return true },
		SetupTick: func(ctx context.Context) (bool, error) {
			setups.Add(1)
			<-ctx.Done()
			return true, ctx.Err()
		},
	}
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("worker failed to join after cancellation")
		}
	}()
	for {
		var refreshed, checked int
		err := store.pool.QueryRow(ctx, `SELECT
 (SELECT count(*) FROM data_pipelines WHERE observed_revision=revision AND observed_at IS NOT NULL AND lease_token='' AND state='blocked' AND reason='database_unavailable'),
 (SELECT count(*) FROM data_connection_checks WHERE state='blocked' AND lease_token='')`).Scan(&refreshed, &checked)
		if err != nil {
			t.Fatalf("pilot queue did not drain: pipelines=%d checks=%d: %v", refreshed, checked, err)
		}
		if refreshed == api.DataPipelinePilotAccountsMax*api.DataPipelinesMax && checked == api.DataPipelinePilotAccountsMax {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if setups.Load() != api.DataPipelineSetupConcurrency {
		t.Fatal("setup queue was not bounded and independent", setups.Load())
	}
}

func TestWorkerDrainsPilotBacklogWhileOtherJobsAreSlow(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	const pipelines = api.DataPipelinePilotAccountsMax * api.DataPipelinesMax
	var claimed, completed atomic.Int32
	started := make(chan struct{}, 3)
	drained := make(chan struct{})
	slow := func(ctx context.Context) (bool, error) {
		started <- struct{}{}
		<-ctx.Done()
		return true, ctx.Err()
	}
	next := func(ctx context.Context) (bool, error) {
		id := claimed.Add(1)
		if id == 1 {
			return slow(ctx)
		}
		if id > pipelines {
			return false, nil
		}
		if completed.Add(1) == pipelines-1 {
			close(drained)
		}
		// Lost intent races must not stop the worker or throttle the backlog.
		return true, ErrConflict
	}
	done := make(chan error, 1)
	go func() {
		done <- runWorkerQueues(ctx,
			workerQueue{api.DataPipelineReconcileConcurrency, next},
			workerQueue{1, slow}, workerQueue{1, slow})
	}()
	for range 3 {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("queues did not start independently")
		}
	}
	select {
	case <-drained:
	case <-time.After(5 * time.Second):
		t.Fatal("slow reconciliation/check/setup stalled the pilot backlog")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation did not join all workers")
	}
}

func TestWorkerBoundsConcurrencyAndJoinsOnFatalError(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var active atomic.Int32
	started := make(chan struct{}, api.DataPipelineReconcileConcurrency+1)
	release := make(chan struct{})
	fatal := errors.New("catalog unavailable")
	next := func(ctx context.Context) (bool, error) {
		active.Add(1)
		defer active.Add(-1)
		started <- struct{}{}
		select {
		case <-release:
			return false, fatal
		case <-ctx.Done():
			return true, ctx.Err()
		}
	}
	done := make(chan error, 1)
	go func() { done <- runWorkerQueues(ctx, workerQueue{api.DataPipelineReconcileConcurrency, next}) }()
	for range api.DataPipelineReconcileConcurrency {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("configured workers did not start")
		}
	}
	select {
	case <-started:
		t.Fatal("worker exceeded its concurrency budget")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-done:
		if !errors.Is(err, fatal) || active.Load() != 0 {
			t.Fatal("fatal error lost or in-flight workers abandoned", err, active.Load())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("fatal error did not stop the worker")
	}
}

func TestWorkerEmptyQueueWaitsAndCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var calls atomic.Int32
	idle := make(chan struct{})
	next := func(context.Context) (bool, error) {
		if calls.Add(1) == 1 {
			close(idle)
		}
		return false, nil
	}
	done := make(chan error, 1)
	go func() { done <- runWorkerQueues(ctx, workerQueue{1, next}) }()
	select {
	case <-idle:
	case <-time.After(5 * time.Second):
		t.Fatal("empty queue did not start")
	}
	time.Sleep(api.DataPipelineWorkerIdleInterval / 10)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || calls.Load() != 1 {
			t.Fatal("empty queue spun or failed to cancel", calls.Load(), err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("idle worker did not cancel")
	}
}

func TestWorkerRetriesCatalogOutageWithoutSpinning(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var calls atomic.Int32
	recovered := make(chan struct{})
	next := func(context.Context) (bool, error) {
		if calls.Add(1) == 1 {
			return false, ErrUnavailable
		}
		select {
		case <-recovered:
		default:
			close(recovered)
		}
		return false, nil
	}
	done := make(chan error, 1)
	go func() { done <- runWorkerQueues(ctx, workerQueue{1, next}) }()
	time.Sleep(api.DataPipelineWorkerIdleInterval / 10)
	if calls.Load() != 1 {
		t.Fatal("catalog outage caused a busy loop", calls.Load())
	}
	select {
	case <-recovered:
	case <-time.After(3 * api.DataPipelineWorkerIdleInterval):
		t.Fatal("worker did not retry the catalog")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not stop")
	}
}

func TestWorkerShutdownPreservesCancellationAfterSanitizedStoreError(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan struct{})
	next := func(ctx context.Context) (bool, error) {
		close(started)
		<-ctx.Done()
		return false, ErrUnavailable // Store claims sanitize database errors.
	}
	done := make(chan error, 1)
	go func() { done <- runWorkerQueues(ctx, workerQueue{1, next}) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("shutdown became a fatal catalog error", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not stop")
	}
}
