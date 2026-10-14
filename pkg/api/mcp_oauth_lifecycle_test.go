package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestCatalogOAuthLifecyclePrivacyAndReminder(t *testing.T) {
	at := time.Now()
	expires := at.Add(5 * time.Minute)
	snapshot := MCPCatalogMonitorSnapshot{Config: MCPCatalogMonitorConfig{Observer: &MCPCatalogObserverConfig{Enabled: true}}, ObserverStatus: &MCPCatalogObserverStatus{FailureReason: "credential_unavailable", OAuth: &MCPCatalogObserverOAuthStatus{State: "connected", ExpiresAt: &expires, Ciphertext: []byte("PRIVATE_TOKEN"), SourceSHA256: "PRIVATE_DIGEST"}}, OAuthLifecycle: &MCPCatalogOAuthLifecycle{LocalState: "disconnected", History: []MCPCatalogOAuthEvent{}, Revocation: &MCPCatalogOAuthRevocation{State: "pending", Config: &MCPCatalogObserverOAuthConfig{ClientID: "PRIVATE_BINDING"}, Ciphertext: []byte("PRIVATE_GRANT"), ClientCiphertext: []byte("PRIVATE_CLIENT"), LeaseID: "PRIVATE_LEASE", LeaseUntil: &at}}}
	public := CatalogOAuthLifecyclePublic(snapshot.OAuthLifecycle)
	status := CatalogObserverStatus(snapshot, at)
	if !status.OAuth.ReauthorizationNeeded {
		t.Fatal("missing pre-expiry reminder")
	}
	encoded, _ := json.Marshal(public)
	for _, key := range []string{"private_", "PRIVATE"} {
		if strings.Contains(string(encoded), key) {
			t.Fatal("private data exposed")
		}
	}
	if snapshot.OAuthLifecycle.Revocation.Config == nil || snapshot.OAuthLifecycle.Revocation.ClientCiphertext == nil || snapshot.ObserverStatus.OAuth.Ciphertext == nil {
		t.Fatal("projection mutated stored credentials")
	}
	snapshot.ObserverStatus.FailureReason = ""
	if CatalogObserverStatus(snapshot, at).OAuth.ReauthorizationNeeded {
		t.Fatal("healthy renewal produced reminder")
	}
	snapshot.ObserverStatus.OAuth.State = "reauthorization_required"
	if !CatalogObserverStatus(snapshot, at).OAuth.ReauthorizationNeeded {
		t.Fatal("invalid grant missing reminder")
	}
}
func TestOAuthRevocationConfigurationValidation(t *testing.T) {
	cfg := MCPCatalogObserverOAuthConfig{TokenEndpoint: "https://provider.example/token", ClientID: "client", RefreshTokenSecretRef: "secret:REFRESH", RevocationEndpoint: "https://provider.example/revoke"}
	if cfg.Validate() != nil {
		t.Fatal("valid configuration rejected")
	}
	for _, url := range []string{"http://provider.example/revoke", "https://provider.example:443/revoke", "https://user@provider.example/revoke", "https://provider.example/revoke?token=x", "https://provider.example/revoke?", "https://provider.example/revoke#fragment"} {
		changed := cfg
		changed.RevocationEndpoint = url
		if changed.Validate() == nil {
			t.Fatal("unsafe endpoint accepted", url)
		}
	}
	cfg.RevocationAuthMethod = "client_secret_post"
	if cfg.Validate() == nil {
		t.Fatal("confidential method without credential accepted")
	}
	cfg.ClientSecretRef = "secret:CLIENT"
	if cfg.Validate() != nil {
		t.Fatal("confidential retirement rejected")
	}
}
