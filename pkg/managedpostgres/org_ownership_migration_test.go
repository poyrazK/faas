package managedpostgres

// adr: 202610171906

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestOrgPostgresOwnershipMigration(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		t.Run(map[bool]string{false: "binding and restore roots", true: "mixed roots refused"}[mixed], func(t *testing.T) {
			ctx, pool := t.Context(), pgtest.OpenMigrated(t)
			raw, err := migrations.FS.ReadFile("20261018003100005_org_managed_postgres_ownership.sql")
			if err != nil {
				t.Fatal(err)
			}
			up, down, ok := strings.Cut(string(raw), "-- +goose Down")
			if !ok {
				t.Fatal("missing rollback")
			}
			if _, err := pool.Exec(ctx, down); err != nil {
				t.Fatal("legacy schema:", err)
			}
			store := state.NewPgStore(pool)
			creator, err := store.CreateAccount(ctx, "migration-postgres@test.example", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			org, err := store.CreateOrg(ctx, state.Org{Slug: "legacy-database-company", Name: "Company", Plan: api.PlanFree})
			if err != nil {
				t.Fatal(err)
			}
			source, restored, personal := uuid.NewString(), uuid.NewString(), uuid.NewString()
			for _, row := range []struct{ id, name string }{{source, "source"}, {restored, "restored"}, {personal, "personal"}} {
				if _, err := pool.Exec(ctx, `INSERT INTO managed_postgres_databases(id,account_id,name,region,postgres_major,service_class,availability,scale_to_zero,storage_limit_bytes,restore_window_seconds,backend_id,backend_fingerprint,state,provider_resource_id,observed_generation) VALUES($1,$2,$3,'us-east-1',17,'development','single_zone',true,1073741824,86400,'primary',$4,'ready',$5,1)`, row.id, creator.ID, row.name, strings.Repeat("a", 64), "provider-"+row.id); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := pool.Exec(ctx, `UPDATE managed_postgres_databases SET restore_source_database_id=$2,restore_source_resource_id=$3,restore_point_in_time=$4 WHERE id=$1`, restored, source, "provider-"+source, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: creator.ID, OrgID: org.ID, Slug: "legacy-database-app"})
			if err != nil {
				t.Fatal(err)
			}
			insert := func(appID string) {
				t.Helper()
				if _, err := pool.Exec(ctx, `INSERT INTO managed_postgres_bindings(id,account_id,database_id,app_id,scope,environment_key,access) VALUES($1,$2,$3,$4,'default','DATABASE_URL','read_write')`, uuid.NewString(), creator.ID, restored, appID); err != nil {
					t.Fatal(err)
				}
			}
			insert(app.ID)
			if mixed {
				other, err := store.CreateApp(ctx, state.App{AccountID: creator.ID, Slug: "personal-database-app"})
				if err != nil {
					t.Fatal(err)
				}
				insert(other.ID)
			}
			_, err = pool.Exec(ctx, up)
			if mixed {
				if err == nil || !strings.Contains(err.Error(), "remediate mixed managed PostgreSQL ownership") {
					t.Fatalf("mixed legacy ownership accepted: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal("ownership expansion:", err)
			}
			catalog, err := NewPostgresStore(pool)
			if err != nil {
				t.Fatal(err)
			}
			for _, id := range []string{source, restored} {
				d, err := catalog.DatabaseOwnership(ctx, id)
				if err != nil || d.OrgID != org.ID || d.AccountID != creator.ID || d.ProviderResourceID != "provider-"+id {
					t.Fatalf("backfill changed identity/root: %+v %v", d, err)
				}
			}
			personalOrg, err := store.OrgByPersonalAccount(ctx, creator.ID)
			if err != nil {
				t.Fatal(err)
			}
			d, err := catalog.DatabaseOwnership(ctx, personal)
			if err != nil || d.OrgID != personalOrg.ID {
				t.Fatalf("unbound database was adopted: %+v %v", d, err)
			}
		})
	}
}
