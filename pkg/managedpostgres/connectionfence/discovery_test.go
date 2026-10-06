// adr: 590
package connectionfence

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgproto3"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
)

func TestConnectionFenceDiscoveryIncludesCompleteCatalogueWithoutMutation(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	actual, err := f.c.DiscoverDatabaseSelection(ctx, f.request.Identity)
	if err != nil || actual.Identity != f.request.Identity || !slices.IsSorted(actual.DatabaseNames) || slices.Contains(actual.DatabaseNames, f.config.MaintenanceDatabase) {
		t.Fatalf("complete native catalogue: %v", err)
	}
	// The fixture administrator does not own these built-ins. They must remain
	// explicit input, alongside the closed database and quoted customer name.
	// Other packages can create/drop unrelated databases on the shared CI cluster.
	for _, name := range append([]string{"postgres", "template0", "template1"}, f.request.DatabaseNames...) {
		if !slices.Contains(actual.DatabaseNames, name) {
			t.Fatal("discovery omitted a native database")
		}
	}
	for i, name := range f.request.DatabaseNames {
		var open bool
		if err := f.root.QueryRow(ctx, "SELECT datallowconn FROM pg_database WHERE datname=$1", name).Scan(&open); err != nil || open != (i == 0) {
			t.Fatalf("read changed source admission: %v", err)
		}
	}
	var installed bool
	if err := f.maintenance.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname='gregale_checkpoint')").Scan(&installed); err != nil || installed {
		t.Fatalf("read installed checkpoint schema: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if result, err := f.c.DiscoverDatabaseSelection(ctx, f.request.Identity); !errors.Is(err, context.Canceled) || !reflect.DeepEqual(result, Request{}) {
		t.Fatalf("canceled discovery returned selection: %v", err)
	}
	if result, err := f.c.DiscoverDatabaseSelection(t.Context(), Identity{}); !errors.Is(err, pgerrors.ErrInvalid) || !reflect.DeepEqual(result, Request{}) {
		t.Fatalf("invalid discovery authority: %v", err)
	}
}

// Drop the rollback protocol message to simulate a lost reply on actual native
// SQL. The socket stays open, so only the caller's deadline bounds recovery.
type discoveryRollbackBlackhole struct {
	io.Writer
	dropped *atomic.Bool
}

func (w discoveryRollbackBlackhole) Write(p []byte) (int, error) {
	if bytes.Contains(p, []byte("rollback\x00")) {
		w.dropped.Store(true)
		return len(p), nil
	}
	return w.Writer.Write(p)
}

func TestConnectionFenceDiscoveryBoundsFailedReadRollback(t *testing.T) {
	f := newFixture(t)
	var dropped atomic.Bool
	config := f.maintenance.Config()
	config.MaxConns, config.MinConns = 1, 0
	config.ConnConfig.BuildFrontend = func(r io.Reader, w io.Writer) *pgproto3.Frontend {
		return pgproto3.NewFrontend(r, discoveryRollbackBlackhole{Writer: w, dropped: &dropped})
	}
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := pool.Acquire(t.Context())
	if err != nil {
		pool.Close()
		t.Fatal(err)
	}
	transport := conn.Conn().PgConn().Conn()
	conn.Release()
	t.Cleanup(func() { _ = transport.Close(); pool.Close() })
	controller, err := New(t.Context(), pool, f.config)
	if err != nil {
		t.Fatal(err)
	}
	// Invalidate native maintenance privacy after authentication. Discovery
	// must refuse the read and roll back its transaction without unbounded IO.
	if _, err := f.root.Exec(t.Context(), "GRANT CONNECT ON DATABASE "+pgx.Identifier{f.config.MaintenanceDatabase}.Sanitize()+" TO "+pgx.Identifier{f.tenant}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		actual, err := controller.DiscoverDatabaseSelection(ctx, f.request.Identity)
		if !reflect.DeepEqual(actual, Request{}) {
			done <- errors.New("failed read returned selection")
			return
		}
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, pgerrors.ErrUnsupported) || !dropped.Load() {
			t.Fatalf("failed read cleanup: dropped=%v err=%v", dropped.Load(), err)
		}
		// Destruction of a broken pooled socket is asynchronous. Prove the only
		// slot is available again rather than sampling an intermediate counter.
		acquireCtx, acquireCancel := context.WithTimeout(t.Context(), 3*time.Second)
		defer acquireCancel()
		replacement, err := pool.Acquire(acquireCtx)
		if err != nil {
			t.Fatalf("cleanup retained pool capacity: %v", err)
		}
		replacement.Release()
	case <-time.After(6 * time.Second):
		_ = transport.Close()
		<-done
		t.Fatal("read-only rollback exceeded the operation deadline")
	}
}
