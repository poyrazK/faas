//go:build !no_pg

// adr: 20261110210000
package state_test

import (
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Slice 3j-4: a deployment's build registry scope is recorded when it is
// created and never changes; a per-node compute login cannot record one; a
// retry keeps the failed row's; malformed scopes are refused.
func TestDeploymentsBuildRegistryScope(t *testing.T) {
	store, owner := lifecycleCompletionPGStore(t)
	ctx := t.Context()
	nodeLogin, login := ownerAuthorityLogin(t, owner, false)
	a := completionNode(t, store, state.NodeLifecycleActive)
	if _, err := owner.Exec(ctx, `insert into compute_node_db_logins (login, node_name) values ($1, $2)`, login, a.Name); err != nil {
		t.Fatal(err)
	}
	acct, err := store.CreateAccount(ctx, "build-scope@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "build-scope", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	scopeOf := func(id string) ([]string, bool) {
		t.Helper()
		var scope []string
		var recorded bool
		if err := owner.QueryRow(ctx, `select coalesce(build_registry_scope, '{}'), build_registry_scope is not null from deployments where id = $1`, id).Scan(&scope, &recorded); err != nil {
			t.Fatal(err)
		}
		return scope, recorded
	}
	create := func(s *state.PgStore, scope []string) (state.Deployment, error) {
		return s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, Status: state.DeployPending,
			SourcePath: "/spool/x.tar.gz", BuildRegistryScope: scope})
	}

	want := []string{"docker.io/library/node", "ghcr.io/acme/base"}
	d, err := create(store, want)
	if err != nil {
		t.Fatal(err)
	}
	if got, recorded := scopeOf(d.ID); !recorded || !reflect.DeepEqual(got, want) {
		t.Fatalf("recorded scope = %v (%v), want %v", got, recorded, want)
	}
	// Nil stays unrecorded; empty is recorded.
	unrecorded, err := create(store, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, recorded := scopeOf(unrecorded.ID); recorded {
		t.Fatal("a nil scope was recorded")
	}
	empty, err := create(store, []string{})
	if err != nil {
		t.Fatal(err)
	}
	if got, recorded := scopeOf(empty.ID); !recorded || len(got) != 0 {
		t.Fatalf("empty scope = %v (%v), want recorded and empty", got, recorded)
	}

	// Nobody changes it afterwards, the schema owner included.
	for name, sql := range map[string]string{
		"widen":  `update deployments set build_registry_scope = array['ghcr.io/victim/all'] where id = $1`,
		"clear":  `update deployments set build_registry_scope = null where id = $1`,
		"record": `update deployments set build_registry_scope = '{}' where id = $1`,
	} {
		id := d.ID
		if name == "record" {
			id = unrecorded.ID
		}
		if _, err := owner.Exec(ctx, sql, id); sqlState(err) != "42501" {
			t.Errorf("%s = %v, want SQLSTATE 42501", name, err)
		}
	}
	// Other updates of the row still work.
	if _, err := owner.Exec(ctx, `update deployments set reason = 'still fine' where id = $1`, d.ID); err != nil {
		t.Fatalf("an unrelated update: %v", err)
	}

	// A per-node login's insert records nothing.
	byNode, err := create(state.NewPgStore(nodeLogin), []string{"ghcr.io/victim/all"})
	if err != nil {
		t.Fatalf("per-node insert: %v", err)
	}
	if got, recorded := scopeOf(byNode.ID); recorded {
		t.Fatalf("per-node insert recorded %v", got)
	}

	// Malformed scopes are refused.
	for name, scope := range map[string][]string{
		"upper case":  {"GHCR.io/acme/base"},
		"no repo":     {"ghcr.io"},
		"null member": nil,
	} {
		if name == "null member" {
			if _, err := owner.Exec(ctx, `insert into deployments (app_id, kind, image_digest, status, build_registry_scope) values ($1, 'tarball', '', 'failed', array[null]::text[])`, app.ID); sqlState(err) != "23514" {
				t.Errorf("%s = %v, want SQLSTATE 23514", name, err)
			}
			continue
		}
		if _, err := create(store, scope); sqlState(err) != "23514" {
			t.Errorf("%s = %v, want SQLSTATE 23514", name, err)
		}
	}

	// A retry builds the same source and keeps the failed row's scope.
	if _, err := owner.Exec(ctx, `update deployments set status = 'failed' where id = $1`, d.ID); err != nil {
		t.Fatal(err)
	}
	retry, err := store.RetryDeploymentFromStage(ctx, d.ID, state.StageSourceDownload)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if got, recorded := scopeOf(retry.ID); !recorded || !reflect.DeepEqual(got, want) {
		t.Fatalf("retry scope = %v (%v), want %v", got, recorded, want)
	}
}
