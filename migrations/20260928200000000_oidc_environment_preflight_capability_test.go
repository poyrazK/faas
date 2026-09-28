//go:build !no_pg

package migrations_test

import (
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrationOIDCEnvironmentPreflightCapability(t *testing.T) {
	pool := pgtest.Open(t)
	migrateUpOnce(t.Context(), t, pool)

	var defaultValue string
	if err := pool.QueryRow(t.Context(), `
		select column_default
		from information_schema.columns
		where table_schema = current_schema()
		  and table_name = 'oidc_exchanged_tokens'
		  and column_name = 'scopes'`).Scan(&defaultValue); err != nil {
		t.Fatalf("read exchanged-token scope default: %v", err)
	}
	if !strings.Contains(defaultValue, "deploy:write") {
		t.Fatalf("OIDC token scope default = %q, want legacy deploy:write", defaultValue)
	}

	var oidcConstraint, apiKeyConstraint string
	if err := pool.QueryRow(t.Context(), `
		select pg_get_constraintdef(oid)
		from pg_constraint
		where conrelid = 'oidc_exchanged_tokens'::regclass
		  and conname = 'oidc_exchanged_tokens_scopes_check'`).Scan(&oidcConstraint); err != nil {
		t.Fatalf("read OIDC scope constraint: %v", err)
	}
	for _, scope := range []string{"deploy:write", "project_environments:read", "project_environments:qualify"} {
		if !strings.Contains(oidcConstraint, scope) {
			t.Errorf("OIDC scope constraint %q does not allow %q", oidcConstraint, scope)
		}
	}
	if err := pool.QueryRow(t.Context(), `
		select pg_get_constraintdef(oid)
		from pg_constraint
		where conrelid = 'api_keys'::regclass
		  and conname = 'api_keys_scopes_vocab_chk'`).Scan(&apiKeyConstraint); err != nil {
		t.Fatalf("read API-key scope constraint: %v", err)
	}
	for _, scope := range []string{"project_environments:read", "project_environments:qualify"} {
		if !strings.Contains(apiKeyConstraint, scope) {
			t.Errorf("API-key scope constraint %q does not allow %q", apiKeyConstraint, scope)
		}
	}

	accountID := "00000000-0000-4000-8000-000000000001"
	if _, err := pool.Exec(t.Context(), `
		insert into accounts (id, email, plan, created_at)
		values ($1::uuid, 'oidc-capability-test@example.com', 'free', now())`, accountID); err != nil {
		t.Fatalf("insert OIDC test account: %v", err)
	}
	expiresAt := time.Now().UTC().Add(5 * time.Minute)
	var defaultScopes []string
	if err := pool.QueryRow(t.Context(), `
		insert into oidc_exchanged_tokens (account_id, token_hash, expires_at, issuer_url, subject, audience)
		values ($1::uuid, $2, $3, 'https://idp.example.test', 'repo:test/project', ARRAY['gregale']::text[])
		returning scopes`, accountID, []byte("legacy-token-hash"), expiresAt).Scan(&defaultScopes); err != nil {
		t.Fatalf("insert legacy-shape OIDC token: %v", err)
	}
	if len(defaultScopes) != 1 || defaultScopes[0] != "deploy:write" {
		t.Fatalf("legacy-shape OIDC token scopes = %v, want [deploy:write]", defaultScopes)
	}
	preflightScopes := []string{"project_environments:read", "project_environments:qualify"}
	var storedScopes []string
	if err := pool.QueryRow(t.Context(), `
		insert into oidc_exchanged_tokens (account_id, token_hash, expires_at, issuer_url, subject, audience, scopes)
		values ($1::uuid, $2, $3, 'https://idp.example.test', 'repo:test/project', ARRAY['gregale']::text[], $4)
		returning scopes`, accountID, []byte("preflight-token-hash"), expiresAt, preflightScopes).Scan(&storedScopes); err != nil {
		t.Fatalf("insert preflight OIDC token: %v", err)
	}
	if len(storedScopes) != len(preflightScopes) || storedScopes[0] != preflightScopes[0] || storedScopes[1] != preflightScopes[1] {
		t.Fatalf("preflight OIDC token scopes = %v, want %v", storedScopes, preflightScopes)
	}
	if _, err := pool.Exec(t.Context(), `
		insert into oidc_exchanged_tokens (account_id, token_hash, expires_at, issuer_url, subject, audience, scopes)
		values ($1::uuid, $2, $3, 'https://idp.example.test', 'repo:test/project', ARRAY['gregale']::text[], ARRAY['secrets:read']::text[])`,
		accountID, []byte("invalid-token-hash"), expiresAt); err == nil {
		t.Fatal("OIDC scope constraint accepted secrets:read")
	}
}
