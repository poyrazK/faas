package state

// adr: 20261026143210

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// A routes analytics read past its budget is cancelled by Postgres and
// reported as ErrRouteAnalyticsTimeout; a read within it returns its rows.
func TestRouteAnalyticsReadBoundsStatements(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	s := NewPgStore(pool)
	_, err := routeAnalyticsReadWithin(t.Context(), s, 50*time.Millisecond, func(db sqlc.DBTX) (int, error) {
		_, err := db.Exec(context.Background(), `select pg_sleep(2)`)
		return 0, err
	})
	if !errors.Is(err, ErrRouteAnalyticsTimeout) {
		t.Fatalf("slow read err = %v, want ErrRouteAnalyticsTimeout", err)
	}
	got, err := routeAnalyticsReadWithin(t.Context(), s, time.Second, func(db sqlc.DBTX) (int, error) {
		var n int
		return n, db.QueryRow(context.Background(), `select 7`).Scan(&n)
	})
	if err != nil || got != 7 {
		t.Fatalf("fast read = %d, %v", got, err)
	}
}
