// adr: 590
package neon

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/connectionfence"
)

type discoveryQueryKey struct{}
type discoveryQueryTracer struct{ after func() }

func (t discoveryQueryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, discoveryQueryKey{}, strings.Contains(data.SQL, "-- name: CheckpointDatabaseNames"))
}
func (t discoveryQueryTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryEndData) {
	if selected, _ := ctx.Value(discoveryQueryKey{}).(bool); selected {
		t.after()
	}
}

func TestCheckpointConnectionDiscoveryNativeInventoriesAllDatabasesReadOnly(t *testing.T) {
	f := newNativeConnectionClosureFixture(t)
	var expected []string
	if err := f.root.QueryRow(t.Context(), `SELECT array_agg(datname::text ORDER BY datname COLLATE "C") FROM pg_database WHERE datname<>$1`, connectionfence.MaintenanceDatabase).Scan(&expected); err != nil {
		t.Fatal(err)
	}
	actual, err := f.p.discoverCheckpointConnections(t.Context(), f.definition, f.maintenance, f.request.CheckpointConnectionIdentity, f.connectPool(t))
	if err != nil || actual.CheckpointConnectionIdentity != f.request.CheckpointConnectionIdentity || !slices.Equal(actual.DatabaseNames, expected) {
		t.Fatalf("native provider selection: %v", err)
	}
	for i, name := range f.request.DatabaseNames {
		var allowed bool
		if err := f.root.QueryRow(t.Context(), "SELECT datallowconn FROM pg_database WHERE datname=$1", name).Scan(&allowed); err != nil || allowed != (i == 0) {
			t.Fatalf("discovery changed native admission: %v", err)
		}
	}
	// Discovery must work before installation and leave no close ledger.
	config := f.config.Copy()
	config.Database = connectionfence.MaintenanceDatabase
	conn, err := pgx.ConnectConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	var installed bool
	if err := conn.QueryRow(t.Context(), "SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname='gregale_checkpoint')").Scan(&installed); err != nil || installed {
		t.Fatalf("discovery installed a barrier: %v", err)
	}
}

func TestCheckpointConnectionDiscoveryNativeRejectsPostReadDrift(t *testing.T) {
	for _, fault := range []string{"placement", "maintenance", "cancel"} {
		t.Run(fault, func(t *testing.T) {
			f := newNativeConnectionClosureFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			called := false
			f.config.Tracer = discoveryQueryTracer{after: func() {
				called = true
				switch fault {
				case "placement":
					f.drift.Store(true)
				case "maintenance":
					role := "grg_ckpt_" + strings.ReplaceAll(f.maintenance.OwnerToken, "-", "")
					if _, err := f.root.Exec(t.Context(), "ALTER ROLE "+pgx.Identifier{role}.Sanitize()+" LOGIN"); err != nil {
						t.Error(err)
					}
				case "cancel":
					cancel()
				}
			}}
			actual, err := f.p.discoverCheckpointConnections(ctx, f.definition, f.maintenance, f.request.CheckpointConnectionIdentity, f.connectPool(t))
			if !called || err == nil || !reflect.DeepEqual(actual, managedpostgres.CheckpointConnectionRequest{}) {
				t.Fatalf("%s postcheck returned usable selection: %v", fault, err)
			}
			if fault == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost: %v", err)
			}
		})
	}
}
