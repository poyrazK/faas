// adr: 20261026100000
package apidserver_test

import (
	"net/url"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// With the platform split from the apps domain, platform links (inbound
// webhook URLs, the dashboard origin) name the platform domain; without it
// they keep the apps domain.
func TestPlatformLinksFollowThePlatformDomain(t *testing.T) {
	srv := apidserver.NewServer(state.NewMemStore(), nil, "gregale.app", nil)
	if got := srv.InboundWebhookURLForTest("tok"); got != "https://api.gregale.app/v1/hooks/tok" {
		t.Fatalf("shared layout inbound URL = %q", got)
	}
	srv.WithPlatformDomain("gregale.dev")
	if got := srv.InboundWebhookURLForTest("tok"); got != "https://api.gregale.dev/v1/hooks/tok" {
		t.Fatalf("split layout inbound URL = %q", got)
	}
	for origin, want := range map[string]bool{
		"https://gregale.dev":      true,
		"https://gregale.app":      false,
		"https://shop.gregale.app": false,
		"http://gregale.dev":       false,
	} {
		u, _ := url.Parse(origin)
		if got := srv.IsWebDashboardOriginForTest(u); got != want {
			t.Errorf("dashboard origin %s = %v, want %v", origin, got, want)
		}
	}
}

func TestPlatformDomainFromConfig(t *testing.T) {
	noEnv := func(string) string { return "" }
	cfg := &apidserver.Config{AppsDomain: "gregale.app"}
	if got := apidserver.PlatformDomainFromConfig(cfg, noEnv); got != "" {
		t.Fatalf("no platform domain = %q, want empty (the apps domain keeps the role)", got)
	}
	cfg.PlatformDomain = "gregale.dev"
	if got := apidserver.PlatformDomainFromConfig(cfg, noEnv); got != "gregale.dev" {
		t.Fatalf("TOML platform domain = %q", got)
	}
	env := func(name string) string {
		if name == "FAAS_PLATFORM_DOMAIN" {
			return "platform.example"
		}
		return ""
	}
	if got := apidserver.PlatformDomainFromConfig(cfg, env); got != "platform.example" {
		t.Fatalf("environment platform domain = %q, want it to win over TOML", got)
	}
}
