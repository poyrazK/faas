package managedpostgres

import (
	"strings"
	"testing"
)

// ADR-20261016151947: only backends that declare a hub run live queries, native and
// Neon alike; the hub URL must be a plain ws:// or wss:// URL.
func TestRegistryLiveHubURL(t *testing.T) {
	provider := &fakeProvider{capabilities: testCapabilities()}
	registry := testRegistry(t, provider, func(c *Config) {
		c.Backends[0].LiveHubURL = "wss://live.internal:9480/{database_id}/live"
	})
	backend, err := registry.Default("us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	if got := backend.LiveHub("3f1c"); got != "wss://live.internal:9480/3f1c/live" {
		t.Fatalf("LiveHub = %q", got)
	}
	if got := testRegistry(t, provider, nil); mustDefault(t, got).LiveHub("x") != "" {
		t.Fatal("a backend without live_hub_url has a hub")
	}
	neon, err := NewRegistry(Config{
		DefaultRegion: "us-east-1",
		Defaults:      map[string]string{"us-east-1": "primary-a"},
		Backends: []BackendConfig{{ID: "primary-a", Driver: "neon", Region: "us-east-1", Namespace: "ns",
			LiveHubURL: "wss://live.internal/{database_id}/live"}},
	}, func(string) string { return "" }, map[string]Factory{
		"neon": func(BackendConfig, func(string) string) (Provider, error) { return provider, nil },
	})
	if err != nil {
		t.Fatalf("a Neon backend with a hub: %v", err)
	}
	if got := mustDefault(t, neon).LiveHub("3f1c"); got != "wss://live.internal/3f1c/live" {
		t.Fatalf("a Neon backend's LiveHub = %q", got)
	}

	for name, mutate := range map[string]func(*BackendConfig){
		"http":        func(b *BackendConfig) { b.LiveHubURL = "https://live.internal/live" },
		"no host":     func(b *BackendConfig) { b.LiveHubURL = "wss:///live" },
		"credentials": func(b *BackendConfig) { b.LiveHubURL = "wss://u:p@live.internal/live" },
		"query":       func(b *BackendConfig) { b.LiveHubURL = "wss://live.internal/live?db={database_id}" },
	} {
		config := Config{
			DefaultRegion: "us-east-1",
			Defaults:      map[string]string{"us-east-1": "primary-a"},
			Backends: []BackendConfig{{
				ID: "primary-a", Driver: "fake", Region: "us-east-1", Namespace: "ns",
			}},
		}
		mutate(&config.Backends[0])
		_, err := NewRegistry(config, func(string) string { return "" }, map[string]Factory{
			"fake": func(BackendConfig, func(string) string) (Provider, error) { return provider, nil },
			"neon": func(BackendConfig, func(string) string) (Provider, error) { return provider, nil },
		})
		if err == nil || !strings.Contains(err.Error(), "live") {
			t.Errorf("%s: NewRegistry error = %v, want a live_hub_url refusal", name, err)
		}
	}
}

func mustDefault(t *testing.T, r *Registry) Backend {
	t.Helper()
	b, err := r.Default("us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	return b
}
