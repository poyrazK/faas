// adr: 20261112160000 — neond reads apid's managed PostgreSQL rows to decide:
// the live resources in a Neon project, and the accounts whose databases, in
// any state, name it.
package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgLiveReadsDatabaseRows(t *testing.T) {
	ctx, pool := t.Context(), pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	a, err := store.CreateAccount(ctx, "neond-a@test.example", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.CreateAccount(ctx, "neond-b@test.example", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct{ account, name, state, resource string }{
		{a.ID, "main", "ready", "p-1"},
		{a.ID, "leaving", "deleting", "p-1/br-2"},
		{b.ID, "branch", "ready", "p-1/br-3"},
		{b.ID, "prefix", "ready", "p-10"},
	} {
		if _, err := pool.Exec(ctx, `INSERT INTO managed_postgres_databases(id,account_id,name,region,postgres_major,service_class,availability,scale_to_zero,storage_limit_bytes,restore_window_seconds,backend_id,backend_fingerprint,state,provider_resource_id,observed_generation) VALUES($1,$2,$3,'us-east-1',17,'development','single_zone',true,1073741824,86400,'primary',$4,$5,$6,1)`,
			uuid.NewString(), row.account, row.name, strings.Repeat("a", 64), row.state, row.resource); err != nil {
			t.Fatalf("insert %s: %v", row.name, err)
		}
	}
	live := pgLive{pool}
	resources, err := live.LiveResourcesInProject(ctx, "p-1")
	slices.Sort(resources)
	if err != nil || !slices.Equal(resources, []string{"p-1", "p-1/br-3"}) {
		t.Fatalf("live resources = %v, %v; want the two live databases, not the deleting one or p-10", resources, err)
	}
	accounts, err := live.AccountsInProject(ctx, "p-1")
	want := []string{a.ID, b.ID}
	slices.Sort(want)
	if err != nil || !slices.Equal(accounts, want) {
		t.Fatalf("accounts = %v, %v; want %v", accounts, err, want)
	}
	if accounts, err := live.AccountsInProject(ctx, "p-2"); err != nil || len(accounts) != 0 {
		t.Fatalf("unnamed project accounts = %v, %v", accounts, err)
	}
}
