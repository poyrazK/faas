package datapipelines

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/datapipeline"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

type organizationBindingFixture struct{ binding managedpostgres.Binding }

func (b *organizationBindingFixture) Create(context.Context, managedpostgres.CreateBindingRequest) (managedpostgres.Binding, error) {
	return managedpostgres.Binding{}, errors.New("unexpected allocation")
}
func (b *organizationBindingFixture) Get(_ context.Context, account, id string) (managedpostgres.Binding, error) {
	if b.binding.ID != id || b.binding.AccountID != account {
		return managedpostgres.Binding{}, managedpostgres.ErrNotFound
	}
	return b.binding, nil
}
func (b *organizationBindingFixture) ListForApp(_ context.Context, account, app, scope string) ([]managedpostgres.AppBindingInventory, error) {
	if b.binding.AccountID != account || b.binding.AppID != app || b.binding.Scope != scope {
		return nil, nil
	}
	return []managedpostgres.AppBindingInventory{{BindingID: b.binding.ID, Access: string(b.binding.Access), State: string(b.binding.State), EnvironmentKey: b.binding.EnvironmentKey}}, nil
}

func TestAuthOrganizationsReadsAuthoritativeMembershipLifecycleAndPinsBinding(t *testing.T) {
	pool := pgtest.OpenDatabase(t)
	acquired, err := pool.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer acquired.Release()
	conn := acquired.Conn()
	if _, err = conn.Exec(t.Context(), `CREATE SCHEMA auth;CREATE SCHEMA gregale_auth;
 CREATE TABLE auth.users(id uuid PRIMARY KEY,deleted_at timestamptz,banned_until timestamptz);
 CREATE TABLE gregale_auth.memberships(org_id uuid NOT NULL,user_id uuid REFERENCES auth.users(id),PRIMARY KEY(org_id,user_id))`); err != nil {
		t.Fatal(err)
	}
	setup, _, job := apiSetupFixture(t)
	b := &organizationBindingFixture{binding: managedpostgres.Binding{ID: uuid.NewString(), AccountID: job.AccountID, AppID: job.Request.AuthAppID, DatabaseID: uuid.NewString(), Access: managedpostgres.CredentialAuth, State: managedpostgres.BindingStateReady, Scope: api.DefaultEnvScope, EnvironmentKey: "DATABASE_URL"}}
	setup.Bindings = b
	calls := 0
	setup.SQL = func(ctx context.Context, account, id string, run func(*pgx.Conn, string) error) error {
		calls++
		if account != job.AccountID || id != b.binding.DatabaseID {
			t.Fatal("snapshot escaped account binding")
		}
		return run(conn, "postgres")
	}
	orgA, orgB, user, banned, deleted := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err = conn.Exec(t.Context(), `INSERT INTO auth.users(id,deleted_at,banned_until) VALUES($1,NULL,NULL),($2,NULL,now()+interval '1 day'),($3,now(),NULL)`, user, banned, deleted); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(t.Context(), `INSERT INTO gregale_auth.memberships(org_id,user_id) VALUES($1,$3),($1,$4),($1,$5),($2,$3)`, orgA, orgB, user, banned, deleted); err != nil {
		t.Fatal(err)
	}
	approved, err := setup.ResolveOrganizations(t.Context(), job.AccountID, job.DatabaseID, api.DataPipelineAuthOrganizations{AuthAppID: job.Request.AuthAppID, Mappings: []api.DataPipelineOrganizationTenant{{OrganizationID: orgA, Tenant: "a"}, {OrganizationID: orgB, Tenant: "b"}}})
	if err != nil {
		t.Fatal(err)
	}
	p := datapipeline.Pipeline{AccountID: job.AccountID, DataPipeline: api.DataPipeline{DatabaseID: job.DatabaseID, Serving: &api.DataPipelineServing{Version: 2, AuthOrganizations: approved}}}
	members, err := setup.OrganizationMembers(t.Context(), p)
	want := []api.DataPipelineTenantMembership{{Subject: user, Tenant: "a"}, {Subject: user, Tenant: "b"}}
	if err != nil || !slices.Equal(members.Members, want) || members.Issuer != "https://shop-auth.example.test/auth/v1" {
		t.Fatal("banned/deleted user or incorrect tenant admitted", members, err)
	}
	setup.AppVisible = func(context.Context, state.Account, state.App) bool { return false }
	if _, err = setup.ResolveOrganizations(t.Context(), job.AccountID, job.DatabaseID, *approved); !errors.Is(err, datapipeline.ErrNotFound) {
		t.Fatal("hidden Auth app accepted for new approval", err)
	}
	if members, err = setup.OrganizationMembers(t.Context(), p); err != nil || !slices.Equal(members.Members, want) {
		t.Fatal("background reconciliation depended on interactive visibility", members, err)
	}
	if _, err = conn.Exec(t.Context(), `DELETE FROM gregale_auth.memberships WHERE org_id=$1 AND user_id=$2`, orgA, user); err != nil {
		t.Fatal(err)
	}
	members, err = setup.OrganizationMembers(t.Context(), p)
	if err != nil || !slices.Equal(members.Members, want[1:]) {
		t.Fatal("authoritative removal not reconciled", members, err)
	}
	previous := calls
	b.binding.ID = uuid.NewString()
	if _, err = setup.OrganizationMembers(t.Context(), p); !errors.Is(err, datapipeline.ErrConflict) || calls != previous {
		t.Fatal("replacement binding read without approval", err, calls)
	}
	b.binding.ID = approved.BindingID
	if _, err = setup.ResolveOrganizations(t.Context(), uuid.NewString(), job.DatabaseID, *approved); !errors.Is(err, datapipeline.ErrNotFound) {
		t.Fatal("foreign Auth app accepted", err)
	}
	if _, err = conn.Exec(t.Context(), `WITH users AS(INSERT INTO auth.users(id) SELECT gen_random_uuid() FROM generate_series(1,257) RETURNING id) INSERT INTO gregale_auth.memberships SELECT $1,id FROM users`, orgA); err != nil {
		t.Fatal(err)
	}
	if _, err = setup.OrganizationMembers(t.Context(), p); !errors.Is(err, datapipeline.ErrUnavailable) {
		t.Fatal("oversized snapshot silently truncated", err)
	}
}
