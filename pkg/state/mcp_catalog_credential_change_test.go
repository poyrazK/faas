package state

import (
	"bytes"
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"strings"
	"testing"
	"time"
)

func TestCredentialChangeCapacityAtomicityAndScopes(t *testing.T) {
	m := NewMemStore()
	m.mcpCatalogMonitorOwners = map[string]string{}
	m.mcpCatalogMonitors = map[string]api.MCPCatalogMonitorSnapshot{}
	account, app := uuid.NewString(), uuid.NewString()
	at := time.Now().UTC()
	until := at.Add(time.Minute)
	m.apps[app] = App{ID: app, AccountID: account, Slug: "credential", Type: AppTypeApp}
	m.mcpCatalogMonitorOwners[app] = account
	cfg := api.MCPCatalogObserverOAuthConfig{TokenEndpoint: "https://provider.example/token", ClientID: "reader", RefreshTokenSecretRef: "secret:REFRESH", RevocationEndpoint: "https://provider.example/revoke"}
	revision := uuid.NewString()
	m.mcpCatalogMonitors[app] = api.MCPCatalogMonitorSnapshot{Config: api.MCPCatalogMonitorConfig{Revision: revision, Observer: &api.MCPCatalogObserverConfig{Paused: true, OAuth: &cfg}}, ObserverStatus: &api.MCPCatalogObserverStatus{LeaseID: uuid.NewString(), LeaseUntil: &until, OAuth: &api.MCPCatalogObserverOAuthStatus{State: "connected", SourceSHA256: strings.Repeat("a", 64), Ciphertext: []byte("old grant")}}}
	key := secretKey{AppID: app, Scope: DefaultEnvScope, Key: "REFRESH"}
	m.secrets[key] = AppSecret{AccountID: account, AppID: app, Scope: DefaultEnvScope, Key: "REFRESH", SecretVersion: 1, Ciphertext: []byte("old secret")}
	for i := 0; i < api.MCPOAuthRetirementMaxOutstanding; i++ {
		phase := &api.MCPCatalogOAuthRevocation{ID: uuid.NewString(), State: "pending", NextAttemptAt: at, Config: &cfg, Ciphertext: []byte("sealed")}
		if err := m.insertRetirementLocked(account, newRetirement(app, "credential", "default", "disconnect", phase, at)); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.UpsertAppSecret(t.Context(), account, app, "REFRESH", []byte("new secret")); !errors.Is(err, ErrConflict) {
		t.Fatal("capacity did not reject mutation", err)
	}
	secret, _ := m.GetAppSecret(t.Context(), account, app, "REFRESH")
	if secret.SecretVersion != 1 || !bytes.Equal(secret.Ciphertext, []byte("old secret")) || m.mcpCatalogMonitors[app].Config.Revision != revision || m.mcpCatalogMonitors[app].ObserverStatus.OAuth == nil || len(m.mcpOAuthRetirements) != api.MCPOAuthRetirementMaxOutstanding {
		t.Fatal("failed mutation lost secret, grant, fence or retirement metadata")
	}
	if _, err := m.DeleteAppSecretInScopeWithRevocation(t.Context(), account, app, DefaultEnvScope, "REFRESH"); !errors.Is(err, ErrConflict) {
		t.Fatal("capacity did not reject deletion", err)
	}
	if err := m.UpsertAppSecretInScope(t.Context(), account, app, "staging", "REFRESH", []byte("scope secret")); err != nil {
		t.Fatal(err)
	}
	if err := m.UpsertAppSecret(t.Context(), account, app, "OTHER", []byte("unrelated")); err != nil {
		t.Fatal(err)
	}
	if m.mcpCatalogMonitors[app].Config.Revision != revision || m.mcpCatalogMonitors[app].ObserverStatus.OAuth == nil {
		t.Fatal("unrelated or scoped mutation affected grant")
	}
	// A paused role still retires its retained grant once capacity is available.
	for id := range m.mcpOAuthRetirements {
		delete(m.mcpOAuthRetirements, id)
	}
	if err := m.UpsertAppSecret(t.Context(), account, app, "REFRESH", []byte("new secret")); err != nil {
		t.Fatal(err)
	}
	if len(m.mcpOAuthRetirements) != 1 || m.mcpCatalogMonitors[app].ObserverStatus.OAuth != nil || m.mcpCatalogMonitors[app].Config.Revision == revision {
		t.Fatal("paused role escaped retirement and generation fence")
	}
}
