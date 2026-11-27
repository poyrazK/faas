//go:build !no_pg

// adr: 20261026150000 — managed database endpoints for bound-app egress.
package state_test

import (
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
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
		// The query orders by the database's collation; compare as a set.
		sort.Slice(mine, func(i, j int) bool { return mine[i].Host < mine[j].Host })
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

func TestPgAPISandboxPostgresBindingUsesDedicatedDatabase(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	accountID, parentID, sourceID := seedLiveDeploy(t, store, ctx, "sandbox-postgres-egress")
	expires := time.Now().Add(time.Hour)
	sandbox, err := store.CreateAppIfUnderQuota(ctx, state.App{
		AccountID: accountID, Slug: "sandbox-postgres-egress", RAMMB: 512, Type: state.AppTypeApp,
		PreviewOfSlug: state.APISandboxPreviewParent("pg-app"), PreviewPrState: state.PreviewPrStateOpen, PreviewExpiresAt: &expires,
		Manifest: state.AppManifest{APISandboxSourceDeploymentID: sourceID},
	}, api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatal(err)
	}
	production := uuid.NewString()
	for _, fixture := range []struct{ database, name, host string }{
		{production, "production-db", "production-db.example.com"},
		{sandbox.ID, sandbox.Slug, "sandbox-db.example.com"},
	} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO managed_postgres_databases (id, account_id, name, region, postgres_major, service_class, availability, backend_id, backend_fingerprint)
			VALUES ($1, $2, $3, 'eu', 17, 'development', 'single_zone', 'test', $4)`,
			fixture.database, accountID, fixture.name, strings.Repeat("a", 64)); err != nil {
			t.Fatal(err)
		}
		if err := store.RecordManagedPostgresDatabaseEndpoint(ctx, fixture.database, fixture.host, 5432); err != nil {
			t.Fatal(err)
		}
	}
	bind := func(appID, database, key, access string) error {
		_, err := pool.Exec(ctx, `
			INSERT INTO managed_postgres_bindings (id, account_id, app_id, database_id, scope, environment_key, access, state, provider_identity_id, credential_ref)
			VALUES (gen_random_uuid(), $1, $2, $3, 'default', $4, $5, 'ready', 'role', 'credential')`,
			accountID, appID, database, key, access)
		return err
	}
	for _, refused := range []struct{ target, database, key, access string }{
		{sandbox.ID, production, "DATABASE_URL", "read_write"},
		{parentID, sandbox.ID, "DATABASE_URL", "read_write"},
		{sandbox.ID, sandbox.ID, "EXTRA_DATABASE_URL", "read_write"},
	} {
		var pgErr *pgconn.PgError
		err := bind(refused.target, refused.database, refused.key, refused.access)
		if !errors.As(err, &pgErr) || pgErr.ConstraintName != "managed_postgres_sandbox_binding" {
			t.Fatalf("sandbox binding for %s was not refused by the isolation guard: %v", refused.key, err)
		}
	}
	if err := bind(sandbox.ID, sandbox.ID, "DATABASE_URL", "read_write"); err != nil {
		t.Fatal(err)
	}
	if err := bind(sandbox.ID, sandbox.ID, "MIGRATION_DATABASE_URL", "migration"); err != nil {
		t.Fatal(err)
	}
	if err := bind(parentID, production, "DATABASE_URL", "read_write"); err != nil {
		t.Fatal(err)
	}
	endpoints, err := store.ManagedPostgresEgressEndpoints(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var sandboxEndpoints []state.ManagedPostgresEgressEndpoint
	for _, endpoint := range endpoints {
		if endpoint.AppID == sandbox.ID {
			sandboxEndpoints = append(sandboxEndpoints, endpoint)
		}
	}
	want := []state.ManagedPostgresEgressEndpoint{{AppID: sandbox.ID, Host: "sandbox-db.example.com", Port: 5432}}
	if !reflect.DeepEqual(sandboxEndpoints, want) {
		t.Fatalf("sandbox database egress = %+v, want %+v", sandboxEndpoints, want)
	}
}
