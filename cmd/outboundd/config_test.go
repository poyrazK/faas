package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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
