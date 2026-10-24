package faas

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func poolURI(password string) string {
	return "postgresql://worker:" + password + "@db.example:6432/app?sslmode=require"
}

func poolValues(password string) map[string]string {
	return map[string]string{"DATABASE_URL": poolURI(password)}
}

type runtimePool struct {
	dsn    string
	closed atomic.Int64
}

func poolOptions(validate func(context.Context, *runtimePool) error) PostgresPoolOptions[*runtimePool] {
	return PostgresPoolOptions[*runtimePool]{Validate: validate, Retire: func(p *runtimePool) error { p.closed.Add(1); return nil }}
}

func newRuntimePool(t *testing.T, validate func(context.Context, *runtimePool) error) *PostgresPoolBinding[*runtimePool] {
	t.Helper()
	b, err := NewPostgresPoolBinding("DATABASE_URL", func(_ context.Context, config PostgresPoolConfig) (*runtimePool, error) {
		return &runtimePool{dsn: config.ConnectionString}, nil
	}, poolOptions(validate))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func poolWait[T any](t *testing.T, channel <-chan T) T {
	t.Helper()
	select {
	case value := <-channel:
		return value
	case <-time.After(3 * time.Second):
		t.Fatal("pool fixture did not progress")
		var zero T
		return zero
	}
}

func drainPool(t *testing.T, b *PostgresPoolBinding[*runtimePool]) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := b.WaitForRetirement(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresPoolValidationAndTransactionDrain(t *testing.T) {
	entered, finish := make(chan struct{}), make(chan struct{})
	b := newRuntimePool(t, func(_ context.Context, p *runtimePool) error {
		if strings.Contains(p.dsn, "private-second") {
			close(entered)
			<-finish
		}
		return nil
	})
	if err := b.Reload(context.Background(), poolValues("private-first")); err != nil {
		t.Fatal(err)
	}
	first, _ := b.Acquire()
	second, _ := b.Acquire()
	old, _ := first.Current()
	done := make(chan error, 1)
	go func() { done <- b.Reload(context.Background(), poolValues("private-second")) }()
	poolWait(t, entered)
	_, pendingErr := b.Acquire()
	oldWhilePending, err := first.Current()
	close(finish)
	if pendingErr == nil || err != nil || oldWhilePending != old || old.closed.Load() != 0 {
		t.Fatal("validation published early or disturbed a transaction")
	}
	if err := poolWait(t, done); err != nil {
		t.Fatal(err)
	}
	next, _ := b.Acquire()
	current, _ := next.Current()
	copyOfFirst := *first
	first.Release()
	copyOfFirst.Release()
	if _, err := copyOfFirst.Current(); err == nil || old.closed.Load() != 0 || current == old {
		t.Fatal("lease copies released another borrower or rotation reused a pool")
	}
	b.Invalidate()
	if _, err := b.Acquire(); err == nil {
		t.Fatal("invalidation allowed acquisition")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := b.WaitForRetirement(cancelled); err == nil || current.closed.Load() != 0 {
		t.Fatal("cancelled wait closed a leased transaction")
	}
	second.Release()
	next.Release()
	drainPool(t, b)
	if old.closed.Load() != 1 || current.closed.Load() != 1 {
		t.Fatal("retirement must run once per owned pool")
	}
}

func TestPostgresPoolValidationFailureReclaimsAndRedacts(t *testing.T) {
	for _, mode := range []string{"error", "panic", "factory-error"} {
		t.Run(mode, func(t *testing.T) {
			pool := &runtimePool{dsn: poolURI("private-secret")}
			var config PostgresPoolConfig
			b, err := NewPostgresPoolBinding("DATABASE_URL", func(_ context.Context, input PostgresPoolConfig) (*runtimePool, error) {
				config = input
				if mode == "factory-error" {
					return pool, errors.New("private-factory")
				}
				return pool, nil
			}, poolOptions(func(context.Context, *runtimePool) error {
				if mode == "panic" {
					panic("private-driver")
				}
				return errors.New("private-driver")
			}))
			if err != nil {
				t.Fatal(err)
			}
			err = b.Reload(context.Background(), poolValues("private-secret"))
			if err == nil || strings.Contains(err.Error(), "private-") {
				t.Fatalf("unsanitized adoption failure: %v", err)
			}
			if _, err := b.Acquire(); err == nil {
				t.Fatal("failed adoption allowed acquisition")
			}
			raw, err := json.Marshal(config)
			if err != nil || strings.Contains(string(raw)+fmt.Sprintf("%v %#v", config, config), "private-") {
				t.Fatal("configuration leaked credentials")
			}
			drainPool(t, b)
			if pool.closed.Load() != 1 {
				t.Fatal("unpublished pool leaked")
			}
		})
	}
}

func TestPostgresPoolLateValidationIsFenced(t *testing.T) {
	for _, mode := range []string{"invalidate", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			entered, finish := make(chan struct{}), make(chan struct{})
			pool := &runtimePool{}
			b, err := NewPostgresPoolBinding("DATABASE_URL", func(context.Context, PostgresPoolConfig) (*runtimePool, error) { return pool, nil },
				poolOptions(func(context.Context, *runtimePool) error { close(entered); <-finish; return nil }))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- b.Reload(ctx, poolValues("private-late")) }()
			poolWait(t, entered)
			if mode == "invalidate" {
				b.Invalidate()
			} else {
				cancel()
				if err := poolWait(t, done); err == nil {
					t.Fatal("ignored validation cancellation")
				}
			}
			_, acquisitionErr := b.Acquire()
			close(finish)
			if acquisitionErr == nil {
				t.Fatal("unvalidated pool became available")
			}
			if mode == "invalidate" && poolWait(t, done) == nil {
				t.Fatal("invalidated validation succeeded")
			}
			drainPool(t, b)
			if pool.closed.Load() != 1 {
				t.Fatal("late pool leaked")
			}
		})
	}
}

func TestPostgresPoolRejectsAliasBeforeValidation(t *testing.T) {
	pool := &runtimePool{}
	var checks atomic.Int64
	b, err := NewPostgresPoolBinding("DATABASE_URL", func(context.Context, PostgresPoolConfig) (*runtimePool, error) { return pool, nil },
		poolOptions(func(context.Context, *runtimePool) error { checks.Add(1); return nil }))
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Reload(context.Background(), poolValues("private-first")); err != nil {
		t.Fatal(err)
	}
	lease, _ := b.Acquire()
	if err := b.Reload(context.Background(), poolValues("private-second")); err == nil || !strings.Contains(err.Error(), "reused an owned pool") {
		t.Fatal("accepted owned alias")
	}
	if checks.Load() != 1 || pool.closed.Load() != 0 {
		t.Fatal("alias validated or closed an old transaction")
	}
	lease.Release()
	drainPool(t, b)
	if pool.closed.Load() != 1 {
		t.Fatal("alias changed retirement ownership")
	}
}

func TestPostgresPoolExplicitURI(t *testing.T) {
	invalid := []string{"\v" + poolURI("private-secret"), "postgres://worker:%1f@db:5432/app", "postgres://worker:secret@db/app", "postgres://worker:secret@%2Ftmp:5432/app", poolURI("private-secret") + "&database=other", "", "host=db user=worker", "https://worker:secret@db/app", "postgres://db/app", "postgres://worker@db/app",
		"postgres://worker:secret@db", "postgres://worker:secret@db/", "postgres://worker:secret@db:0/app",
		"postgres://worker:secret@db:65536/app", poolURI("private-secret") + "#fragment", poolURI("private-secret") + "#",
		poolURI("private-secret") + "&UsEr=other", poolURI("private-secret") + "&host=other", "postgres://worker:%00@db/app",
		"postgres://worker:%ZZ@db/app", poolURI("private-secret") + "\n"}
	var calls atomic.Int64
	b, err := NewPostgresPoolBinding("DATABASE_URL", func(_ context.Context, config PostgresPoolConfig) (*runtimePool, error) {
		calls.Add(1)
		return &runtimePool{dsn: config.ConnectionString}, nil
	}, poolOptions(func(context.Context, *runtimePool) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	for i, value := range invalid {
		if err := b.Reload(context.Background(), map[string]string{"DATABASE_URL": value}); err == nil {
			t.Errorf("accepted invalid URI case %d", i)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid configuration invoked driver")
	}
	for _, value := range []string{poolURI("private-secret"), "postgres://work%20er:p%40ss@[::1]:5432/app%2Fdb?sslmode=disable"} {
		if err := b.Reload(context.Background(), map[string]string{"DATABASE_URL": value}); err != nil {
			t.Fatal(err)
		}
		b.Invalidate()
		drainPool(t, b)
	}
	if _, err := NewPostgresPoolBinding("DATABASE_URL", func(context.Context, PostgresPoolConfig) (*runtimePool, error) { return nil, nil }, PostgresPoolOptions[*runtimePool]{}); err == nil {
		t.Fatal("validation and retirement must be required")
	}
}
