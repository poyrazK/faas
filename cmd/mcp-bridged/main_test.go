// adr: 20261026195952
package main

import (
	"flag"
	"slices"
	"testing"
)

func parse(t *testing.T, args ...string) (config, error) {
	t.Helper()
	fs := flag.NewFlagSet("mcp-bridged", flag.ContinueOnError)
	f := registerFlags(fs)
	if err := fs.Parse(args); err != nil {
		t.Fatal(err)
	}
	return f.config()
}

func TestConfigDefaultsToThePlatformAPIHost(t *testing.T) {
	t.Setenv("FAAS_APPS_DOMAIN", "gregale.dev")
	t.Setenv("FAAS_PLATFORM_DOMAIN", "")
	cfg, err := parse(t, "--state-key-file", "/run/credentials/key", "--oauth-redirect-uri", "https://claude.ai/api/mcp/auth_callback", "--api-base", "http://127.0.0.1:8081/")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.resourceURL != "https://api.gregale.dev/mcp" || cfg.apiBase != "http://127.0.0.1:8081" || cfg.cliPath != "/opt/faas/current/bin/gregale" || !slices.Equal(cfg.toolsets, []string{"core", "provision"}) {
		t.Fatalf("config = %+v", cfg)
	}
	t.Setenv("FAAS_PLATFORM_DOMAIN", "gregale.io")
	if cfg, err := parse(t, "--state-key-file", "k", "--oauth-redirect-uri", "http://localhost/callback"); err != nil || cfg.resourceURL != "https://api.gregale.io/mcp" {
		t.Fatalf("platform domain ignored: %+v %v", cfg, err)
	}
}

func TestConfigRefusesUnsafeSettings(t *testing.T) {
	base := []string{"--state-key-file", "/k", "--oauth-redirect-uri", "http://localhost/callback"}
	for name, args := range map[string][]string{
		"public listener": append([]string{"--listen", "0.0.0.0:8787"}, base...),
		"no key":          {"--oauth-redirect-uri", "http://localhost/callback"},
		"no redirects":    {"--state-key-file", "/k"},
		"relative cli":    append([]string{"--cli", "gregale"}, base...),
		"long timeout":    append([]string{"--timeout", "2m"}, base...),
		"unknown toolset": append([]string{"--tools", "everything"}, base...),
	} {
		if _, err := parse(t, args...); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
