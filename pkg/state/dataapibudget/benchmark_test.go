package dataapibudget_test

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/state/dataapibudget"
)

// DATABASE_URL must be a disposable PostgreSQL server. The benchmark creates
// and drops only its own schema; counters for distinct subjects share one app.
func BenchmarkSharedBudgetIndependentSubjects(b *testing.B) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		b.Skip("DATABASE_URL required")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		b.Fatal(err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("budget_bench_%d", time.Now().UnixNano())
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		b.Fatal(err)
	}
	defer func() { _, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE") }()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		b.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.MaxConns = 16
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		b.Fatal(err)
	}
	defer pool.Close()
	const account = "11111111-1111-4111-8111-111111111111"
	const app = "22222222-2222-4222-8222-222222222222"
	_, err = pool.Exec(ctx, `CREATE TABLE apps(id uuid PRIMARY KEY,account_id uuid NOT NULL,deleted_at timestamptz);
 CREATE TABLE data_api_shared_budgets(app_id uuid NOT NULL,budget_key text NOT NULL,window_start timestamptz NOT NULL,window_end timestamptz NOT NULL,requests bigint NOT NULL,PRIMARY KEY(app_id,budget_key));
 INSERT INTO apps VALUES('22222222-2222-4222-8222-222222222222','11111111-1111-4111-8111-111111111111',null)`)
	if err != nil {
		b.Fatal(err)
	}
	store := dataapibudget.New(pool)
	for i := range 128 {
		if _, err = store.Take(ctx, account, app, fmt.Sprintf("%064x", i), 1000000, 3600); err != nil {
			b.Fatal(err)
		}
	}
	var subject atomic.Uint64
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		key := fmt.Sprintf("%064x", subject.Add(1)%128)
		for pb.Next() {
			d, err := store.Take(ctx, account, app, key, 1000000, 3600)
			if err != nil || !d.Allowed {
				b.Errorf("budget admission: %+v %v", d, err)
				return
			}
		}
	})
	b.StopTimer()
}
