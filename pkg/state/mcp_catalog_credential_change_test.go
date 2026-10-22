package state_test

import (
	"bytes"
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"strings"
	"testing"
	"time"
)

func TestCredentialChangeCapacityAtomicityAndScopes(t *testing.T) {
	m := state.NewMemStore()
	(*m.McpCatalogMonitorOwners()) = map[string]string{}
	(*m.McpCatalogMonitors()) = map[string]api.MCPCatalogMonitorSnapshot{}
	account, app := uuid.NewString(), uuid.NewString()
	at := time.Now().UTC()
	until := at.Add(time.Minute)
	(*m.Apps())[app] = state.App{ID: app, AccountID: account, Slug: "credential", Type: state.AppTypeApp}
	(*m.McpCatalogMonitorOwners())[app] = account
	cfg := api.MCPCatalogObserverOAuthConfig{TokenEndpoint: "https://provider.example/token", ClientID: "reader", RefreshTokenSecretRef: "secret:REFRESH", RevocationEndpoint: "https://provider.example/revoke"}
	revision := uuid.NewString()
	(*m.McpCatalogMonitors())[app] = api.MCPCatalogMonitorSnapshot{Config: api.MCPCatalogMonitorConfig{Revision: revision, Observer: &api.MCPCatalogObserverConfig{Paused: true, OAuth: &cfg}}, ObserverStatus: &api.MCPCatalogObserverStatus{LeaseID: uuid.NewString(), LeaseUntil: &until, OAuth: &api.MCPCatalogObserverOAuthStatus{State: "connected", SourceSHA256: strings.Repeat("a", 64), Ciphertext: []byte("old grant")}}}
	key := state.SecretKey{AppID: app, Scope: state.DefaultEnvScope, Key: "REFRESH"}
	(*m.Secrets())[key] = state.AppSecret{AccountID: account, AppID: app, Scope: state.DefaultEnvScope, Key: "REFRESH", SecretVersion: 1, Ciphertext: []byte("old secret")}
	for i := 0; i < api.MCPOAuthRetirementMaxOutstanding; i++ {
		phase := &api.MCPCatalogOAuthRevocation{ID: uuid.NewString(), State: "pending", NextAttemptAt: at, Config: &cfg, Ciphertext: []byte("sealed")}
		if err := m.InsertRetirementLocked(account, state.NewRetirement(app, "credential", "default", "disconnect", phase, at)); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.UpsertAppSecret(t.Context(), account, app, "REFRESH", []byte("new secret")); !errors.Is(err, state.ErrConflict) {
		t.Fatal("capacity did not reject mutation", err)
	}
	secret, _ := m.GetAppSecret(t.Context(), account, app, "REFRESH")
	if secret.SecretVersion != 1 || !bytes.Equal(secret.Ciphertext, []byte("old secret")) || (*m.McpCatalogMonitors())[app].Config.Revision != revision || (*m.McpCatalogMonitors())[app].ObserverStatus.OAuth == nil || len((*m.McpOAuthRetirements())) != api.MCPOAuthRetirementMaxOutstanding {
		t.Fatal("failed mutation lost secret, grant, fence or retirement metadata")
	}
	if _, err := m.DeleteAppSecretInScopeWithRevocation(t.Context(), account, app, state.DefaultEnvScope, "REFRESH"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("capacity did not reject deletion", err)
	}
	if err := m.UpsertAppSecretInScope(t.Context(), account, app, "staging", "REFRESH", []byte("scope secret")); err != nil {
		t.Fatal(err)
	}
	if err := m.UpsertAppSecret(t.Context(), account, app, "OTHER", []byte("unrelated")); err != nil {
		t.Fatal(err)
	}
	if (*m.McpCatalogMonitors())[app].Config.Revision != revision || (*m.McpCatalogMonitors())[app].ObserverStatus.OAuth == nil {
		t.Fatal("unrelated or scoped mutation affected grant")
	}
	// A paused role still retires its retained grant once capacity is available.
	for id := range *m.McpOAuthRetirements() {
		delete((*m.McpOAuthRetirements()), id)
	}
	if err := m.UpsertAppSecret(t.Context(), account, app, "REFRESH", []byte("new secret")); err != nil {
		t.Fatal(err)
	}
	if len((*m.McpOAuthRetirements())) != 1 || (*m.McpCatalogMonitors())[app].ObserverStatus.OAuth != nil || (*m.McpCatalogMonitors())[app].Config.Revision == revision {
		t.Fatal("paused role escaped retirement and generation fence")
	}
}
