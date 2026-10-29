package sched_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/sched"
)

// A schedd stopped while the loop's first LISTEN is still being acquired
// drains cleanly; it is not a failed start.
func TestLoopRunStoppedBeforeFirstListenIsClean(t *testing.T) {
	// pgxpool connects lazily, so this pool reaches no server: the
	// cancelled context ends the first acquire.
	pool, err := pgxpool.New(context.Background(), "postgres://faas@127.0.0.1:1/faas?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sched.NewLoop(pool, nil, sched.TestLog()).Run(ctx); err != nil {
		t.Fatalf("Run on a stopped context = %v, want nil", err)
	}
}
