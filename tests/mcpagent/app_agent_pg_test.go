package mcpagent_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAppAgentCredentialPostgresRotationPreservesBinding(t *testing.T) {
	store := state.NewPgStore(pgtest.OpenMigrated(t))
	account, err := store.CreateAccount(t.Context(), "mcp-agent-pg@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "app-agent-pg", Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	scopes, err := api.NormalizeAppAgentScopes([]string{api.ScopeDeployWrite, api.ScopeEnvWrite, api.ScopeUsageRead})
	if err != nil {
		t.Fatal(err)
	}
	_, hash, err := api.GenerateDeployToken()
	if err != nil {
		t.Fatal(err)
	}
	token, err := store.CreateDeployToken(t.Context(), account.ID, app.ID, hash, "coding-agent", scopes, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	_, principal, err := store.AuthenticateDeployToken(t.Context(), hash)
	if err != nil {
		t.Fatal(err)
	}
	if principal.AppID != app.ID || !slices.Equal(principal.Scopes, scopes) {
		t.Fatal("native PostgreSQL principal lost its app binding or scopes")
	}
	_, replacementHash, err := api.GenerateDeployToken()
	if err != nil {
		t.Fatal(err)
	}
	rotated, _, err := store.RotateDeployToken(t.Context(), account.ID, app.ID, token.ID, replacementHash, "", time.Now().Add(time.Hour), 0)
	if err != nil {
		t.Fatal(err)
	}
	if rotated.AppID != app.ID || !slices.Equal(rotated.Scopes, scopes) {
		t.Fatal("rotation widened the app binding or scopes")
	}
	if _, _, err := store.AuthenticateDeployToken(t.Context(), hash); !errors.Is(err, state.ErrAPIKeyRevoked) {
		t.Fatalf("old credential remains usable: %v", err)
	}
	if _, _, err := store.AuthenticateDeployToken(t.Context(), replacementHash); err != nil {
		t.Fatal(err)
	}
}

func TestAppAgentUsageScopeMigrationReplayAndRollback(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	account, app := newBatchApp(t, store)
	scopes, err := api.NormalizeAppAgentScopes([]string{api.ScopeUsageRead})
	if err != nil {
		t.Fatal(err)
	}
	_, hash, err := api.GenerateDeployToken()
	if err != nil {
		t.Fatal(err)
	}
	token, err := store.CreateDeployToken(t.Context(), account.ID, app.ID, hash, "usage-reader", scopes, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := migrations.FS.ReadFile("20261103211147001_app_agent_usage_scope.sql")
	if err != nil {
		t.Fatal(err)
	}
	beforeDown, down, ok := strings.Cut(string(raw), "-- +goose Down")
	if !ok {
		t.Fatal("missing Down")
	}
	_, up, ok := strings.Cut(beforeDown, "-- +goose Up")
	if !ok {
		t.Fatal("missing Up")
	}
	for range 2 {
		if _, err := pool.Exec(t.Context(), up); err != nil {
			t.Fatalf("replay Up: %v", err)
		}
	}
	if _, err := pool.Exec(t.Context(), `UPDATE deploy_tokens SET scopes=array_append(scopes,'admin') WHERE id=$1`, token.ID); err == nil {
		t.Fatal("database accepted an administrative app-agent scope")
	}
	if _, err := pool.Exec(t.Context(), down); err == nil {
		t.Fatal("rollback accepted an existing usage credential")
	}
	if _, _, err := store.AuthenticateDeployToken(t.Context(), hash); err != nil {
		t.Fatalf("refused rollback changed the credential: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `DELETE FROM deploy_tokens WHERE id=$1`, token.ID); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{down, down, up, up} {
		if _, err := pool.Exec(t.Context(), body); err != nil {
			t.Fatalf("safe rollback or replay: %v", err)
		}
	}
}
