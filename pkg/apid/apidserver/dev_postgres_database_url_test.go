package apidserver_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

// A DATABASE_URL secret left by an earlier `gregale dev --env-file` blocked
// the --postgres binding: its credential write replaces only a row the same
// binding owns, so it failed and retried hourly while the app kept the old
// database, and the CLI said nothing. --postgres now refuses up front for
// either of the database's keys, and works once the secret is removed.
func TestDevPostgresRefusesAnExistingDatabaseSecret(t *testing.T) {
	for _, key := range []string{"DATABASE_URL", "MIGRATION_DATABASE_URL"} {
		t.Run(key, func(t *testing.T) {
			srv, store, acct, app := devPostgresTestApp(t, "dev-db-"+map[string]string{"DATABASE_URL": "url", "MIGRATION_DATABASE_URL": "mig"}[key])
			ctx := t.Context()
			if err := store.UpsertAppSecret(ctx, acct.ID, app.ID, key, []byte("sealed")); err != nil {
				t.Fatal(err)
			}
			_, err := srv.EnsureDevPostgresForTest(ctx, acct, app)
			if got, ok := apidserver.DevPostgresSecretConflictKey(err); !ok || got != key {
				t.Fatalf("ensure with a customer %s = %v, want that key's refusal", key, err)
			}
			problem := api.ErrDevPostgresDatabaseURLSecret(app.Slug, key)
			if problem.Status != http.StatusConflict || problem.Code != api.CodeDevPostgresDatabaseURLSecret {
				t.Fatalf("problem = %d %s", problem.Status, problem.Code)
			}
			if err := store.DeleteAppSecret(ctx, acct.ID, app.ID, key); err != nil {
				t.Fatal(err)
			}
			if _, err := srv.EnsureDevPostgresForTest(ctx, acct, app); err != nil {
				t.Fatalf("ensure after removing the secret: %v", err)
			}
		})
	}
}

// A read_write login cannot create tables, so a developer database also gets
// a migration binding that only release tasks receive; the serving app keeps
// DATABASE_URL. Renewing creates nothing new, and teardown removes both
// bindings with the database (a leftover binding would refuse the delete
// and strand the environment in teardown).
func TestDevPostgresBindsAMigrationCredential(t *testing.T) {
	srv, _, acct, app := devPostgresTestApp(t, "dev-db-migrate")
	ctx := t.Context()
	postgres, err := srv.EnsureDevPostgresForTest(ctx, acct, app)
	if err != nil {
		t.Fatal(err)
	}
	if postgres.EnvironmentKey != "DATABASE_URL" || postgres.MigrationEnvironmentKey != "MIGRATION_DATABASE_URL" {
		t.Fatalf("response names %s and %q, want DATABASE_URL and MIGRATION_DATABASE_URL", postgres.EnvironmentKey, postgres.MigrationEnvironmentKey)
	}
	bindings := devPostgresBindings(t, srv, acct, postgres.DatabaseID)
	if !slices.Equal(bindings, []string{"DATABASE_URL=read_write", "MIGRATION_DATABASE_URL=migration"}) {
		t.Fatalf("bindings = %v", bindings)
	}
	if _, err := srv.EnsureDevPostgresForTest(ctx, acct, app); err != nil {
		t.Fatalf("renew: %v", err)
	}
	if again := devPostgresBindings(t, srv, acct, postgres.DatabaseID); !slices.Equal(again, bindings) {
		t.Fatalf("bindings after renewal = %v, want %v", again, bindings)
	}
	app.PreviewOfSlug = "dev-db-migrate-src"
	if err := srv.CleanupDevPostgresForTest(ctx, app); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if left := devPostgresBindings(t, srv, acct, postgres.DatabaseID); len(left) != 0 {
		t.Fatalf("bindings after cleanup = %v, want none", left)
	}
}

func devPostgresTestApp(t *testing.T, slug string) (*apidserver.Server, state.Store, state.Account, state.App) {
	t.Helper()
	env := apidserver.NewSourceRefTestServer(t, api.PlanPro, slug, 9034)
	ctx := t.Context()
	store := *env.Store()
	srv := *env.Srv()
	acct, err := store.AccountByID(ctx, *env.AcctID())
	if err != nil {
		t.Fatal(err)
	}
	apidserver.AttachTestManagedPostgres(t, srv, 3)
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: slug + "-app", RAMMB: 512, Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	return srv, store, acct, app
}

// devPostgresBindings lists the live bindings of a database as KEY=access.
func devPostgresBindings(t *testing.T, srv *apidserver.Server, acct state.Account, databaseID string) []string {
	t.Helper()
	bindings, err := (*srv.ManagedPostgresBindings()).List(t.Context(), acct.ID, databaseID)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, b := range bindings {
		if b.State == managedpostgres.BindingStateDeleted || b.State == managedpostgres.BindingStateDeleting {
			continue
		}
		out = append(out, b.EnvironmentKey+"="+string(b.Access))
	}
	slices.Sort(out)
	return out
}

// The developer database reports ready only once both its bindings are: the
// CLI holds the first sync on it, and the first release command needs
// MIGRATION_DATABASE_URL as much as the first boot needs DATABASE_URL.
func TestDevPostgresBindingsStateIsReadyOnlyWhenEveryBindingIs(t *testing.T) {
	const (
		provisioning = managedpostgres.BindingStateProvisioning
		ready        = managedpostgres.BindingStateReady
		failed       = managedpostgres.BindingStateFailed
	)
	for _, tc := range []struct {
		name   string
		states []managedpostgres.BindingState
		want   string
	}{
		{"none yet", nil, "provisioning"},
		{"both ready", []managedpostgres.BindingState{ready, ready}, "ready"},
		{"migration still provisioning", []managedpostgres.BindingState{ready, provisioning}, "provisioning"},
		{"database url still provisioning", []managedpostgres.BindingState{provisioning, ready}, "provisioning"},
		{"failed wins", []managedpostgres.BindingState{provisioning, failed}, "failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := apidserver.DevPostgresBindingsState(tc.states); got != tc.want {
				t.Fatalf("state = %q, want %q", got, tc.want)
			}
		})
	}
}
