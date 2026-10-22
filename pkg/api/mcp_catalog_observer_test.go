package api

import (
	"testing"
	"time"
)

func TestMCPCatalogObserverIntentAndStatus(t *testing.T) {
	cfg := MCPCatalogObserverConfig{Enabled: true}
	if p := cfg.NormalizeAndValidate(90, ""); p != nil {
		t.Fatal(p)
	}
	if cfg.IntervalSeconds != 30 || cfg.TimeoutSeconds != 10 || cfg.Endpoint != "/mcp" {
		t.Fatal("defaults missing")
	}
	for _, path := range []string{"https://outside.example/mcp", "//outside.example/mcp", "/mcp?token=x", "/mcp#x", "/\\outside"} {
		bad := cfg
		bad.Endpoint = path
		if bad.NormalizeAndValidate(90, "") == nil {
			t.Fatalf("unsafe target accepted: %s", path)
		}
	}
	bad := cfg
	bad.SecretRef = "TOKEN_VALUE"
	if bad.NormalizeAndValidate(90, "") == nil {
		t.Fatal("secret value accepted as reference")
	}
	if cfg.NormalizeAndValidate(90, "LOCAL_TOKEN") == nil || cfg.NormalizeAndValidate(60, "") == nil {
		t.Fatal("mixed credential modes or unsafe freshness accepted")
	}
	now := time.Now().UTC()
	past := now.Add(-time.Minute)
	future := now.Add(time.Minute)
	snapshot := MCPCatalogMonitorSnapshot{Config: MCPCatalogMonitorConfig{Enabled: true, Observer: &cfg}, ObserverStatus: &MCPCatalogObserverStatus{State: "checking", LeaseID: "private-fence", LeaseUntil: &future}}
	got := CatalogObserverStatus(snapshot, now)
	if got.State != "checking" || got.LeaseID != "" || snapshot.ObserverStatus.LeaseID != "private-fence" {
		t.Fatal("lease projection changed storage or exposed fencing token")
	}
	snapshot.ObserverStatus.LeaseUntil = &past
	if CatalogObserverStatus(snapshot, now).State != "overdue" {
		t.Fatal("dead observer remained checking")
	}
	cfg.Paused = true
	snapshot.Config.Enabled = false
	if CatalogObserverStatus(snapshot, now).State != "paused" {
		t.Fatal("pause intent ignored")
	}
	cfg.Paused = false
	cfg.Enabled = false
	if CatalogObserverStatus(snapshot, now).State != "stopped" {
		t.Fatal("stop intent ignored")
	}
}

func TestCatalogOAuthConfiguration(t *testing.T) {
	valid := MCPCatalogObserverOAuthConfig{TokenEndpoint: "https://provider.example/token", ClientID: "public", RefreshTokenSecretRef: "secret:REFRESH"}
	if p := valid.Validate(); p != nil {
		t.Fatal(p)
	}
	for _, endpoint := range []string{"http://provider.example/token", "https://user:pass@provider.example/token", "https://provider.example/token?q=secret", "https://provider.example/token#fragment", "https://provider.example:443/token", "//provider.example/token"} {
		c := valid
		c.TokenEndpoint = endpoint
		if c.Validate() == nil {
			t.Fatal("unsafe endpoint accepted", endpoint)
		}
	}
	c := valid
	c.RefreshTokenSecretRef = ""
	if c.Validate() == nil {
		t.Fatal("missing refresh secret accepted")
	}
	observer := MCPCatalogObserverConfig{OAuth: &valid, SecretRef: "secret:ACCESS"}
	if observer.NormalizeAndValidate(90, "") == nil {
		t.Fatal("mixed credential modes accepted")
	}
}

func TestGuidedOAuthIntentAndPrivacy(t *testing.T) {
	cfg := MCPCatalogObserverOAuthConfig{Guided: true, TokenEndpoint: "https://provider.example/token", AuthorizationEndpoint: "https://provider.example/authorize", ClientID: "reader", Scopes: []string{"read"}}
	if p := cfg.Validate(); p != nil {
		t.Fatal(p)
	}
	cfg.RefreshTokenSecretRef = "secret:REFRESH"
	if cfg.Validate() == nil {
		t.Fatal("mixed guided and manual credentials accepted")
	}
	cfg.RefreshTokenSecretRef = ""
	cfg.Scopes = []string{"read admin"}
	if cfg.Validate() == nil {
		t.Fatal("invalid scope token accepted")
	}
	snapshot := MCPCatalogMonitorSnapshot{Config: MCPCatalogMonitorConfig{Observer: &MCPCatalogObserverConfig{Enabled: true}}, ObserverStatus: &MCPCatalogObserverStatus{OAuthFlow: &MCPCatalogOAuthFlow{Ciphertext: []byte("private")}}}
	if CatalogObserverStatus(snapshot, time.Now()).OAuthFlow != nil || snapshot.ObserverStatus.OAuthFlow == nil {
		t.Fatal("private flow projection leaked or mutated storage")
	}
}

func TestOAuthProfileIntent(t *testing.T) {
	cfg := MCPCatalogObserverOAuthConfig{Guided: true, AuthorizationEndpoint: "https://provider.example/authorize", TokenEndpoint: "https://provider.example/token", ClientID: "reader", Profile: "oidc", Scopes: []string{"read"}, CallbackPort: 34567, Resource: "https://mcp.example/mcp"}
	if p := cfg.Validate(); p != nil {
		t.Fatal(p)
	}
	if got := cfg.RequestedScopes(); len(got) != 3 || got[1] != "openid" || got[2] != "offline_access" || len(cfg.Scopes) != 1 {
		t.Fatal("OIDC scope derivation mutates intent or omits consent scopes")
	}
	for _, edit := range []func(*MCPCatalogObserverOAuthConfig){
		func(c *MCPCatalogObserverOAuthConfig) { c.Profile = "unknown" }, func(c *MCPCatalogObserverOAuthConfig) { c.CallbackPort = 65536 },
		func(c *MCPCatalogObserverOAuthConfig) { c.TokenAuthMethod = "client_secret_post" }, func(c *MCPCatalogObserverOAuthConfig) { c.Resource = "https://user:secret@mcp.example/mcp" },
		func(c *MCPCatalogObserverOAuthConfig) { c.DiscoveryURL = "https://provider.example/metadata" }, func(c *MCPCatalogObserverOAuthConfig) { c.TokenAuthMethod = "private_key_jwt" },
	} {
		bad := cfg
		edit(&bad)
		if bad.Validate() == nil {
			t.Fatal("unsupported compatibility configuration accepted")
		}
	}
}

func TestBrowserOAuthRedirectValidation(t *testing.T) {
	for _, tc := range []struct {
		url   string
		valid bool
	}{
		{"https://console.example/dashboard/mcp/oauth/callback", true},
		{"http://console.example/dashboard/mcp/oauth/callback", false},
		{"https://console.example:443/dashboard/mcp/oauth/callback", false},
		{"https://user@console.example/dashboard/mcp/oauth/callback", false},
		{"https://console.example/dashboard/mcp/oauth/callback?next=bad", false},
		{"https://console.example/dashboard/mcp/oauth/callback#fragment", false},
		{"https://console.example/dashboard/mcp/oauth/%63allback", false},
		{"https://console.example/callback", false},
	} {
		if got := ValidMCPOAuthBrowserRedirect(tc.url); got != tc.valid {
			t.Errorf("redirect %q: %v", tc.url, got)
		}
	}
	cfg := MCPCatalogObserverOAuthConfig{TokenEndpoint: "https://provider.example/token", ClientID: "client", RefreshTokenSecretRef: "secret:REFRESH", BrowserRedirectURI: "https://console.example/dashboard/mcp/oauth/callback"}
	if cfg.Validate() == nil {
		t.Fatal("non-guided hosted callback accepted")
	}
}
