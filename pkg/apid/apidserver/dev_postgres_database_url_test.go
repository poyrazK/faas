package apidserver_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// A DATABASE_URL secret left by an earlier `gregale dev --env-file` blocked
// the --postgres binding: its credential write replaces only a row the same
// binding owns, so it failed and retried hourly while the app kept the old
// database, and the CLI said nothing. --postgres now refuses up front, and
// works once the secret is removed.
func TestDevPostgresRefusesAnExistingDatabaseURLSecret(t *testing.T) {
	env := apidserver.NewSourceRefTestServer(t, api.PlanPro, "dev-db-url", 9034)
	ctx := t.Context()
	store := *env.Store()
	srv := *env.Srv()
	acct, err := store.AccountByID(ctx, *env.AcctID())
	if err != nil {
		t.Fatal(err)
	}
	apidserver.AttachTestManagedPostgres(t, srv, 3)
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "dev-db-url-app", RAMMB: 512, Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAppSecret(ctx, acct.ID, app.ID, "DATABASE_URL", []byte("sealed")); err != nil {
		t.Fatal(err)
	}

	if _, err := srv.EnsureDevPostgresForTest(ctx, acct, app); !errors.Is(err, apidserver.ErrDevPostgresDatabaseURLSecret) {
		t.Fatalf("ensure with a customer DATABASE_URL = %v, want the DATABASE_URL refusal", err)
	}
	problem := api.ErrDevPostgresDatabaseURLSecret(app.Slug)
	if problem.Status != http.StatusConflict || problem.Code != api.CodeDevPostgresDatabaseURLSecret {
		t.Fatalf("problem = %d %s", problem.Status, problem.Code)
	}

	if err := store.DeleteAppSecret(ctx, acct.ID, app.ID, "DATABASE_URL"); err != nil {
		t.Fatal(err)
	}
	postgres, err := srv.EnsureDevPostgresForTest(ctx, acct, app)
	if err != nil {
		t.Fatalf("ensure after removing the secret: %v", err)
	}
	if postgres == nil || postgres.EnvironmentKey != "DATABASE_URL" {
		t.Fatalf("postgres = %+v, want a DATABASE_URL binding", postgres)
	}
}
