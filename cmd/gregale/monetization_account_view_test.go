package main_test

import (
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// The selling commands follow the account's capability view, like the
// realtime preview: an account API monetization is released to sees them
// without FAAS_API_MONETIZATION_ENABLED in its shell.
func TestMonetizationFollowsAccountView(t *testing.T) {
	t.Setenv("FAAS_API_MONETIZATION_ENABLED", "")
	t.Setenv("FAAS_MCP_GATEWAY_PREVIEW_ENABLED", "")
	if main.MonetizationInView(true) {
		t.Fatal("tier defaults (logged out, unconfirmed) must hide API monetization")
	}
	if !main.MonetizationInView(false) {
		t.Fatal("an account view that hides nothing must show API monetization")
	}
	if main.MonetizationInView(false, "api-monetization") {
		t.Fatal("an account apid hides api-monetization from must not see it")
	}

	in := main.VerbsDiscoveredBy("mcp")
	for _, verb := range []string{"stats", "listing"} {
		if !slices.Contains(in, verb) {
			t.Errorf("discovery for a released account leaves out mcp %s: %v", verb, in)
		}
	}
	if slices.Contains(in, "auth") {
		t.Errorf("the gateway preview verbs keep their own gate: %v", in)
	}
	out := main.VerbsDiscoveredBy("mcp", "api-monetization")
	if slices.Contains(out, "stats") || slices.Contains(out, "listing") {
		t.Errorf("discovery for an unreleased account lists the selling verbs: %v", out)
	}
	if !slices.Contains(main.VerbsDiscoveredBy("consumers"), "rate-cards") {
		t.Error("discovery for a released account leaves out consumers rate-cards")
	}
	if slices.Contains(main.VerbsDiscoveredBy("consumers", "api-monetization"), "rate-cards") {
		t.Error("discovery for an unreleased account lists consumers rate-cards")
	}
}

func TestMonetizationVerbsRunForReleasedAccount(t *testing.T) {
	api := &capabilitiesAPI{}
	server := httptest.NewServer(api)
	defer server.Close()
	clearCapabilityReleaseAll(t)
	t.Setenv("FAAS_API_MONETIZATION_ENABLED", "")
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	t.Setenv("FAAS_COMPLETION_CACHE_PATH", filepath.Join(t.TempDir(), "cache.json"))

	api.setHidden("api-monetization")
	code, stderr := runMCPStats(t)
	if code == 0 || !strings.Contains(stderr, `unknown mcp subcommand "stats"`) {
		t.Fatalf("unreleased mcp stats = %d %q, want unknown subcommand", code, stderr)
	}

	main.ResetCapabilityView()
	t.Setenv("FAAS_COMPLETION_CACHE_PATH", filepath.Join(t.TempDir(), "cache.json"))
	api.setHidden()
	if _, stderr := runMCPStats(t); strings.Contains(stderr, "unknown mcp subcommand") {
		t.Fatalf("released mcp stats must run: %q", stderr)
	}
}

// runMCPStats runs `mcp stats` with os.Stderr captured: the dispatch refusal
// writes there directly.
func runMCPStats(t *testing.T) (int, string) {
	t.Helper()
	stderr, restore := main.CaptureStderr(t)
	code, _, pkgErr := runCLI(t, "mcp", "stats", "my-server")
	restore()
	return code, stderr.String() + pkgErr
}
