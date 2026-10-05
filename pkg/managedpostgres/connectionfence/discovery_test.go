// adr: 590
package connectionfence

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

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
