package main

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

func TestLoadConfigDefaultsWhenMissing(t *testing.T) {
	c, err := LoadConfig(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.ListenAddr != "127.0.0.1:8095" || c.MetricsAddr != "127.0.0.1:9108" || c.MaxBodyBytes <= 0 {
		t.Fatalf("defaults = %#v", c)
	}
}

func TestPoliciesHashesTokenAndValidatesIntegration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outboundd.toml")
	contents := `
[integrations.payments]
id = "00000000-0000-0000-0000-000000000001"
account_id = "00000000-0000-0000-0000-000000000010"
name = "payments"
origin = "https://api.example.com"
token_env = "PAYMENTS_TOKEN"
app_ids = ["00000000-0000-0000-0000-000000000020"]
rate_per_second = 50
burst = 50
max_in_flight = 20
request_timeout = 30000000000
enabled = true
`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	items, err := c.Policies(func(name string) string {
		if name == "PAYMENTS_TOKEN" {
			return "secret"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Record.Policy.RequestTimeout != 30*time.Second {
		t.Fatalf("policies = %#v", items)
	}
	if !items[0].Record.Policy.AllowsApp("00000000-0000-0000-0000-000000000020") {
		t.Fatal("app binding missing")
	}
	if !items[0].Record.Policy.Enabled {
		t.Fatal("enabled should default to true")
	}
}

func TestPoliciesLoadOptionalProviderAuthorizationFromPrivateEnvironment(t *testing.T) {
	const key = "provider-secret"
	cfg := &Config{Integrations: map[string]IntegrationConfig{
		"payments": {
			ID:                       "00000000-0000-0000-0000-000000000001",
			AccountID:                "00000000-0000-0000-0000-000000000010",
			Origin:                   "https://api.example.com",
			TokenEnv:                 "GATEWAY_TOKEN",
			ProviderAuthorizationEnv: "PROVIDER_AUTHORIZATION",
			AllowedMethods:           []string{"GET"}, AllowedPathPrefixes: []string{"/v1/widgets"},
			AppIDs:        []string{"00000000-0000-0000-0000-000000000020"},
			RatePerSecond: 50,
			Burst:         50,
			MaxInFlight:   20,
		},
	}}
	lookup := func(name string) string {
		switch name {
		case "GATEWAY_TOKEN":
			return "gateway-token"
		case "PROVIDER_AUTHORIZATION":
			return "Bearer " + key
		default:
			return ""
		}
	}
	items, err := cfg.Policies(lookup)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].providerAuthorization != "Bearer "+key {
		t.Fatal("provider authorization was not loaded")
	}
	if items[0].Record.Policy.Origin.String() != "https://api.example.com" {
		t.Fatal("integration policy was not constructed")
	}
	if items[0].Record.Policy.ProviderAuthMode != "managed" {
		t.Fatal("managed authentication mode was not set")
	}
	withoutRoutes := cfg.Integrations["payments"]
	withoutRoutes.AllowedPathPrefixes = nil
	cfg.Integrations["payments"] = withoutRoutes
	if _, err := cfg.Policies(lookup); err == nil {
		t.Fatal("managed credential without explicit route policy was accepted")
	}

	cfg.Integrations["payments"] = IntegrationConfig{
		ID:                       "00000000-0000-0000-0000-000000000001",
		AccountID:                "00000000-0000-0000-0000-000000000010",
		Origin:                   "https://api.example.com",
		TokenEnv:                 "GATEWAY_TOKEN",
		ProviderAuthorizationEnv: "MISSING_PROVIDER_AUTHORIZATION",
		AllowedMethods:           []string{"GET"}, AllowedPathPrefixes: []string{"/v1/widgets"},
		AppIDs:        []string{"00000000-0000-0000-0000-000000000020"},
		RatePerSecond: 50,
		Burst:         50,
		MaxInFlight:   20,
	}
	if _, err := cfg.Policies(lookup); err == nil {
		t.Fatal("missing provider credential was accepted")
	}
}

func TestManagedIntegrationRequiresLocalWorkloadIdentityJWKS(t *testing.T) {
	cfg := &Config{Integrations: map[string]IntegrationConfig{
		"payments": {
			ID: "00000000-0000-0000-0000-000000000001", AccountID: "00000000-0000-0000-0000-000000000010",
			Origin: "https://api.example.com", TokenEnv: "GATEWAY_TOKEN",
			ProviderAuthorizationEnv: "PROVIDER_AUTHORIZATION",
			AllowedMethods:           []string{"GET"}, AllowedPathPrefixes: []string{"/v1/widgets"},
			AppIDs:        []string{"00000000-0000-0000-0000-000000000020"},
			RatePerSecond: 50, Burst: 50, MaxInFlight: 20,
		},
	}}
	items, err := cfg.Policies(func(name string) string {
		if name == "GATEWAY_TOKEN" {
			return "gateway-token"
		}
		return "Bearer provider-key"
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.IdentityVerifier(items); err == nil {
		t.Fatal("managed integration started without trusted JWKS")
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := workloadidentity.NewSigner(key, workloadidentity.DefaultIssuer, "test-key", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	jwks, err := json.Marshal(signer.JWKS())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "identity.jwks.json")
	if err := os.WriteFile(path, jwks, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.WorkloadIdentityJWKSPath = path
	verifier, err := cfg.IdentityVerifier(items)
	if err != nil {
		t.Fatal(err)
	}
	assertion, err := signer.Mint(time.Now(), "account", "app", "instance", "gregale:outbound:"+items[0].Record.Policy.ID)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := verifier.Verify(assertion.AccessToken, items[0].Record.Policy.ID)
	if err != nil || identity.AppID != "app" {
		t.Fatalf("verified identity = %+v, %v", identity, err)
	}
}
