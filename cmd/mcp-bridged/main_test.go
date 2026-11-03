// adr: 20261026195952
package main

import (
	"flag"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/mcpmanagement"
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
		"public metrics":  append([]string{"--metrics-listen", ":9110"}, base...),
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

// The metrics listener serves the bridge's aggregate series beside the
// process's, so memory can be watched against the unit's MemoryMax.
func TestMetricsListenerServesBridgeAndProcessMetrics(t *testing.T) {
	side, err := startSideListeners(slog.New(slog.DiscardHandler), config{metricsListen: "127.0.0.1:0"}, mcpmanagement.NewTelemetry(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(side.close)
	response, err := http.Get("http://" + side.addrs["metrics"] + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"gregale_management_mcp_active", "go_goroutines"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("metrics lack %s", want)
		}
	}
}
