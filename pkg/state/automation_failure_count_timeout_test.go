package state

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type blockedFailureCountDB struct{}
type blockedFailureCountRow struct{ ctx context.Context }

func (blockedFailureCountDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	panic("unexpected Exec")
}
func (blockedFailureCountDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	panic("unexpected Query")
}
func (blockedFailureCountDB) QueryRow(ctx context.Context, _ string, _ ...any) pgx.Row {
	return blockedFailureCountRow{ctx: ctx}
}
func (r blockedFailureCountRow) Scan(...any) error { <-r.ctx.Done(); return r.ctx.Err() }

func TestRetainedAutomationEventsHasBoundedAdvisoryCount(t *testing.T) {
	start := time.Now()
	_, incomplete := retainedAutomationEvents(t.Context(), blockedFailureCountDB{}, "app", "name")
	if !incomplete || time.Since(start) > automationFailureRetainedCountTimeout+time.Second {
		t.Fatalf("blocked advisory count: incomplete=%v elapsed=%s", incomplete, time.Since(start))
	}
}
