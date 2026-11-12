package dataapibudget_test

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state/dataapibudget"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestSharedBudgetSerializesReplicasAndSurvivesNewStore(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()
	const account = "11111111-1111-4111-8111-111111111111"
	const app = "22222222-2222-4222-8222-222222222222"
	if _, err := pool.Exec(ctx, `CREATE TABLE apps(id uuid PRIMARY KEY,account_id uuid NOT NULL,deleted_at timestamptz); INSERT INTO apps(id,account_id) VALUES('22222222-2222-4222-8222-222222222222','11111111-1111-4111-8111-111111111111')`); err != nil {
		t.Fatal(err)
	}
	raw, err := migrations.FS.ReadFile("20261112121000000_data_api_shared_budgets.sql")
	if err != nil {
		t.Fatal(err)
	}
	ddl, _, _ := strings.Cut(string(raw), "-- +goose Down")
	ddl = strings.ReplaceAll(ddl, "GRANT SELECT, INSERT, UPDATE, DELETE ON data_api_shared_budgets TO faas_runtime;", "")
	if _, err = pool.Exec(ctx, ddl); err != nil {
		t.Fatal(err)
	}
	var allowed atomic.Int32
	var wg sync.WaitGroup
	key := strings.Repeat("a", 64)
	// Independent store objects model replicas; the database enforces one total.
	for range 20 {
		wg.Go(func() {
			d, err := dataapibudget.New(pool).Take(ctx, account, app, key, 7, 3600)
			if err != nil {
				t.Error(err)
			} else if d.Allowed {
				allowed.Add(1)
			}
		})
	}
	wg.Wait()
	if allowed.Load() != 7 {
		t.Fatalf("allowed %d, want 7", allowed.Load())
	}
	store := dataapibudget.New(pool)
	if d, err := store.Take(ctx, account, app, key, 7, 3600); err != nil || d.Allowed {
		t.Fatalf("restart lost count: %+v %v", d, err)
	}
	if d, err := store.Take(ctx, account, app, strings.Repeat("b", 64), 7, 3600); err != nil || !d.Allowed {
		t.Fatalf("other tenant: %+v %v", d, err)
	}
	if _, err := store.Take(ctx, "33333333-3333-4333-8333-333333333333", app, key, 7, 3600); !errors.Is(err, dataapibudget.ErrNotFound) {
		t.Fatalf("foreign account: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE data_api_shared_budgets SET window_start=now()-interval '2 hours',window_end=now()-interval '1 hour'`); err != nil {
		t.Fatal(err)
	}
	if d, err := store.Take(ctx, account, app, key, 7, 3600); err != nil || !d.Allowed {
		t.Fatalf("expired window: %+v %v", d, err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO data_api_shared_budgets(app_id,budget_key,window_start,window_end,requests) SELECT $1,lpad(to_hex(i),64,'0'),now(),now()+interval '1 hour',1 FROM generate_series(1,$2) i`, app, api.DataAPISharedBudgetMaxKeys-1); err != nil {
		t.Fatal(err)
	}
	if d, err := store.Take(ctx, account, app, strings.Repeat("c", 64), 7, 3600); err != nil || d.Allowed {
		t.Fatalf("tracking bound: %+v %v", d, err)
	}
	if d, err := store.Take(ctx, account, app, key, 7, 3600); err != nil || !d.Allowed {
		t.Fatalf("tracking pressure evicted existing tenant: %+v %v", d, err)
	}
}
