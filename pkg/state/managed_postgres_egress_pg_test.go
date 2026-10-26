//go:build !no_pg

// adr: 20261026150000 — managed database endpoints for bound-app egress.
package state_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgManagedPostgresEgressEndpoints(t *testing.T) {
	st, pool, ctx := pgStoreWithPool(t)
	acct, app := seedPgAccountAndApp(t, st, ctx)
	database := uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO managed_postgres_databases(id,account_id,name,region,postgres_major,service_class,availability,backend_id,backend_fingerprint) VALUES($1,$2,'egressdb','eu',17,'development','single_zone','test',$3)`,
		database, acct.ID, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"EP-1-Pooler.example.com.", "ep-1-pooler.example.com", "ep-1.example.com"} {
		if err := st.RecordManagedPostgresDatabaseEndpoint(ctx, database, host, 5432); err != nil {
			t.Fatalf("record %q: %v", host, err)
		}
	}
	for _, bad := range []struct {
		database, host string
		port           int
	}{{database, "bad_host", 5432}, {database, "ep-1.example.com", 0}, {"not-a-uuid", "ep-1.example.com", 5432}} {
		if err := st.RecordManagedPostgresDatabaseEndpoint(ctx, bad.database, bad.host, bad.port); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("record %+v = %v, want ErrInvalidArgument", bad, err)
		}
	}
	list := func() []state.ManagedPostgresEgressEndpoint {
		t.Helper()
		got, err := st.ManagedPostgresEgressEndpoints(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var mine []state.ManagedPostgresEgressEndpoint
		for _, e := range got {
			if e.AppID == app.ID {
				mine = append(mine, e)
			}
		}
		return mine
	}
	binding := uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO managed_postgres_bindings(id,account_id,database_id,app_id,scope,environment_key,access) VALUES($1,$2,$3,$4,'default','DATABASE_URL','data_api')`,
		binding, acct.ID, database, app.ID); err != nil {
		t.Fatal(err)
	}
	if got := list(); len(got) != 0 {
		t.Fatalf("a provisioning binding opens nothing: %+v", got)
	}
	if _, err := pool.Exec(ctx, `UPDATE managed_postgres_bindings SET state='ready', provider_identity_id='role-1', credential_ref='ref-1' WHERE id=$1`, binding); err != nil {
		t.Fatal(err)
	}
	want := []state.ManagedPostgresEgressEndpoint{{AppID: app.ID, Host: "ep-1-pooler.example.com", Port: 5432}, {AppID: app.ID, Host: "ep-1.example.com", Port: 5432}}
	if got := list(); !reflect.DeepEqual(got, want) {
		t.Fatalf("ready binding endpoints = %+v, want %+v", got, want)
	}
	if _, err := pool.Exec(ctx, `UPDATE managed_postgres_bindings SET state='deleted', deleted_at=now() WHERE id=$1`, binding); err != nil {
		t.Fatal(err)
	}
	if got := list(); len(got) != 0 {
		t.Fatalf("a deleted binding opens nothing: %+v", got)
	}
}
