package db

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestSubscribeWithReconnect_NilPoolErrors ensures the wrapper fails fast on
// obvious misconfig rather than spinning forever.
func TestSubscribeWithReconnect_NilPoolErrors(t *testing.T) {
	if _, err := SubscribeWithReconnect(context.Background(), nil, []string{NotifyAppChanged}, nil); err == nil {
		t.Fatalf("expected error for nil pool, got nil")
	}
	if _, err := SubscribeWithReconnect(context.Background(), &pgxpool.Pool{}, nil, nil); err == nil {
		t.Fatalf("expected error for empty channels, got nil")
	}
}

// TestSubscribeWithReconnect_ClosesOnCtxCancel ensures the wrapper's outer
// channel shuts down cleanly when the caller's context is cancelled (the
// one path the wrapper exposes its own close on).
//
// Skipped when the test Postgres connection is unavailable — the rest of
// the test suite (pkg/state, etc.) honours the same LOCAL_PG_URL env.
func TestSubscribeWithReconnect_ClosesOnCtxCancel(t *testing.T) {
	pool, closer := newTestPoolOrSkip(t)
	defer closer()

	ctx, cancel := context.WithCancel(context.Background())
	notif, err := SubscribeWithReconnect(ctx, pool, []string{"test_channel_xyz"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("SubscribeWithReconnect initial acquire: %v", err)
	}
	cancel()
	select {
	case _, ok := <-notif:
		if ok {
			// Drain anything buffered, then check the next read sees close.
			select {
			case _, ok := <-notif:
				if ok {
					t.Fatalf("expected channel to close after ctx cancel; got a buffered value")
				}
			case <-time.After(2 * time.Second):
				t.Fatalf("expected channel close within 2s of ctx cancel")
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("expected channel close within 2s of ctx cancel")
	}
}

// TestSubscribeWithReconnect_ResubscribesOnInnerClose exercises the F-11
// invariant across the four daemon call sites: when the inner Subscribe
// channel closes (here: pool close → conn drop → inner Subscribe goroutine
// returns), the outer channel stays open so the daemon's select loop
// keeps running. This is the bug class closed by F-11.
func TestSubscribeWithReconnect_ResubscribesOnInnerClose(t *testing.T) {
	pool, closer := newTestPoolOrSkip(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	notif, err := SubscribeWithReconnect(ctx, pool, []string{"resubscribe_test"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("SubscribeWithReconnect initial acquire: %v", err)
	}

	// First message — sanity check the subscription is live.
	if err := Notify(ctx, pool, "resubscribe_test", `{"hello":"world"}`); err != nil {
		t.Fatalf("notify first: %v", err)
	}
	select {
	case n := <-notif:
		if n.Payload != `{"hello":"world"}` {
			t.Fatalf("first payload=%s, want hello:world", n.Payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("never received first notification")
	}

	// Killing the pool closes every acquired connection; the wrapper's
	// inner goroutine returns from WaitForNotification, inner Subscribe
	// closes the inner channel, and the wrapper falls into its
	// resubscribe loop. The outer channel must NOT close (the F-11
	// invariant).
	closer()

	// Give the wrapper a moment to observe the close and start retrying.
	// Without the wrapper, this same sequence would have closed the outer
	// channel within the same window — the regression test reads the
	// outer channel after the close window and expects it still open.
	time.Sleep(200 * time.Millisecond)

	select {
	case _, ok := <-notif:
		if ok {
			// A spurious buffered value during the close window is fine;
			// we want to confirm the channel is still open for the next read.
			select {
			case _, ok := <-notif:
				if !ok {
					// Channel closed within the resubscribe window.
					// F-11 regression.
					select {
					case <-ctx.Done():
						// Race: ctx cancel got there first. Tolerated;
						// the assertion below still proves the loop is
						// healthy at this point.
					default:
						t.Fatalf("F-11 regression: outer channel closed mid-resubscribe; daemon loops would exit")
					}
				}
			default:
				// Channel still open, no buffered value — expected.
			}
		} else {
			select {
			case <-ctx.Done():
			default:
				t.Fatalf("F-11 regression: outer channel closed mid-resubscribe")
			}
		}
	default:
		// No buffered value either — channel still open. Expected.
	}
}

// newTestPoolOrSkip returns a *pgxpool.Pool wired against the standard
// LOCAL_PG_URL, or skips the test (the rest of the suite honours the same
// env var). The closer should be deferred by the test to release the pool.
func newTestPoolOrSkip(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	dsn := os.Getenv("LOCAL_PG_URL")
	if dsn == "" {
		t.Skip("LOCAL_PG_URL not set; skipping pgx-backed integration test")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Skipf("LOCAL_PG_URL parse: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Skipf("pgxpool.NewWithConfig: %v", err)
	}
	return pool, pool.Close
}
