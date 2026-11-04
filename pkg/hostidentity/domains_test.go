package hostidentity

import (
	"strings"
	"testing"
)

func mustDomains(t *testing.T, platform, apps, deploy string, aliases []string, mode string) Domains {
	t.Helper()
	d, err := NewDomains(platform, apps, deploy, aliases, mode)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// A deployment that sets only the apps domain keeps the pre-ADR layout:
// the platform hosts are that domain's apex, api. and operations.
func TestDomainsDefaultLayoutIsUnchanged(t *testing.T) {
	d := mustDomains(t, "", "Gregale.DEV.", "", nil, "")
	if d.Platform != "gregale.dev" || d.Apps != "gregale.dev" || d.Deploy != DefaultDeployDomain || len(d.AppAliases) != 0 || d.AliasRedirect {
		t.Fatalf("defaults = %+v", d)
	}
	for host, want := range map[string]bool{"gregale.dev": true, "api.gregale.dev": true, "operations.gregale.dev": true, "shop.gregale.dev": false} {
		if got := d.IsPlatformHost(host); got != want {
			t.Errorf("IsPlatformHost(%q) = %v", host, got)
		}
	}
	for host, want := range map[string]bool{"shop.gregale.dev": true, "api.gregale.dev": false, "gregale.dev": false, "a.b.gregale.dev": false, "shop.example.com": false} {
		if got := d.IsAppHost(host); got != want {
			t.Errorf("IsAppHost(%q) = %v", host, got)
		}
	}
	if got, ok := d.CanonicalAppHost("shop.gregale.dev"); !ok || got != "shop.gregale.dev" {
		t.Errorf("CanonicalAppHost = %q %v", got, ok)
	}
}

// The migration layout: the platform stays on gregale.dev, apps move to
// gregale.app, and old app URLs under gregale.dev keep resolving.
func TestDomainsSplitLayoutCanonicalizesAliases(t *testing.T) {
	d := mustDomains(t, "gregale.dev", "gregale.app", "gregale.app", []string{"gregale.dev", "gregale.dev"}, "")
	if len(d.AppAliases) != 1 {
		t.Fatalf("aliases not deduplicated: %v", d.AppAliases)
	}
	if !d.IsPlatformHost("api.gregale.dev") || d.IsPlatformHost("api.gregale.app") {
		t.Fatal("platform hosts follow the platform domain")
	}
	for host, want := range map[string]string{
		"shop.gregale.app":       "shop.gregale.app",
		"shop.gregale.dev":       "shop.gregale.app",
		"pr-7-shop.gregale.dev":  "pr-7-shop.gregale.app",
		"deploy-3-x.gregale.dev": "deploy-3-x.gregale.app",
	} {
		if got, ok := d.CanonicalAppHost(host); !ok || got != want {
			t.Errorf("CanonicalAppHost(%q) = %q %v, want %q", host, got, ok, want)
		}
	}
	for _, host := range []string{"api.gregale.dev", "gregale.dev", "operations.gregale.dev", "a.b.gregale.app", "gregale.app.evil.com", "shop.example.com"} {
		if got, ok := d.CanonicalAppHost(host); ok {
			t.Errorf("CanonicalAppHost(%q) = %q, want no app host", host, got)
		}
	}
	if d.AliasRedirect {
		t.Fatal("serve mode redirects")
	}
	if got := strings.Join(d.OwnedDomains(), ","); got != "gregale.dev,gregale.app" {
		t.Fatalf("OwnedDomains = %s", got)
	}
	for host, want := range map[string]bool{"x.gregale.app": true, "gregale.dev": true, "a.b.gregale.dev": true, "shop.example.com": false, "notgregale.dev": false} {
		if got := d.Owns(host); got != want {
			t.Errorf("Owns(%q) = %v", host, got)
		}
	}
}

func TestDomainsAliasRedirectMode(t *testing.T) {
	d := mustDomains(t, "gregale.dev", "gregale.app", "", []string{"gregale.dev"}, "redirect")
	if !d.AliasRedirect {
		t.Fatal("redirect mode not set")
	}
}

// Rules a customer stored under the old apps domain keep applying after the
// move: the canonical host's alias names are read too, and an old-host
// pattern fences and invalidates the canonical host.
func TestDomainsAliasHostsAndPatterns(t *testing.T) {
	d := mustDomains(t, "gregale.dev", "gregale.app", "gregale.app", []string{"gregale.dev", "old.example"}, "")
	if got := strings.Join(d.AliasHosts("shop.gregale.app"), ","); got != "shop.gregale.dev,shop.old.example" {
		t.Fatalf("AliasHosts = %s", got)
	}
	for _, host := range []string{"gregale.app", "a.b.gregale.app", "shop.gregale.dev", "shop.example.com"} {
		if got := d.AliasHosts(host); len(got) != 0 {
			t.Errorf("AliasHosts(%q) = %v, want none", host, got)
		}
	}
	// The platform's own hosts are never an app's alias name.
	if got := strings.Join(d.AliasHosts("api.gregale.app"), ","); got != "api.old.example" {
		t.Fatalf("AliasHosts(api.gregale.app) = %s", got)
	}
	for pattern, want := range map[string]string{
		"shop.gregale.dev":  "shop.gregale.app",
		" Shop.Old.Example": "shop.gregale.app",
		"*.gregale.dev":     "*.gregale.app",
		"shop.gregale.app":  "shop.gregale.app",
		"api.gregale.dev":   "api.gregale.dev",
		"gregale.dev":       "gregale.dev",
		"a.b.gregale.dev":   "a.b.gregale.dev",
		"*":                 "*",
		"@app":              "@app",
		"shop.example.com":  "shop.example.com",
	} {
		if got := d.CanonicalHostPattern(pattern); got != want {
			t.Errorf("CanonicalHostPattern(%q) = %q, want %q", pattern, got, want)
		}
	}
	shared := mustDomains(t, "", "gregale.dev", "", nil, "")
	if got := shared.AliasHosts("shop.gregale.dev"); got != nil {
		t.Fatalf("no aliases: AliasHosts = %v", got)
	}
}

func TestNewDomainsRejectsBadLayouts(t *testing.T) {
	for name, tc := range map[string]struct {
		platform, apps, deploy string
		aliases                []string
		mode                   string
	}{
		"alias inside apps":    {apps: "gregale.app", aliases: []string{"x.gregale.app"}},
		"apps inside alias":    {apps: "x.gregale.app", aliases: []string{"gregale.app"}},
		"alias equals apps":    {apps: "gregale.app", aliases: []string{"x.example"}, mode: "teleport"},
		"aliases without apps": {aliases: []string{"gregale.dev"}},
		"invalid platform":     {platform: "not a domain", apps: "gregale.app"},
		"single label":         {apps: "localhost"},
		"too many aliases":     {apps: "gregale.app", aliases: []string{"a.example", "b.example", "c.example", "d.example", "e.example", "f.example", "g.example", "h.example", "i.example"}},
	} {
		if _, err := NewDomains(tc.platform, tc.apps, tc.deploy, tc.aliases, tc.mode); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := NewDomains("", "", "", nil, ""); err != nil {
		t.Fatalf("no apps domain (dev single-box) rejected: %v", err)
	}
}

func TestDomainsFromEnvPrefersEnvironment(t *testing.T) {
	env := map[string]string{
		EnvPlatformDomain:      "gregale.dev",
		EnvAppsDomain:          "gregale.app",
		EnvAppsDomainAliases:   "gregale.dev, old.example",
		EnvAppsDomainAliasMode: "redirect",
	}
	d, err := DomainsFromEnv(func(k string) string { return env[k] }, "ignored.example", FileConfig{PlatformDomain: "file.example", DeployDomain: "deploy.example", AppsDomainAliases: []string{"file-alias.example"}})
	if err != nil {
		t.Fatal(err)
	}
	if d.Platform != "gregale.dev" || d.Apps != "gregale.app" || d.Deploy != "deploy.example" || strings.Join(d.AppAliases, ",") != "gregale.dev,old.example" || !d.AliasRedirect {
		t.Fatalf("resolved = %+v", d)
	}
	d, err = DomainsFromEnv(func(string) string { return "" }, "gregale.dev", FileConfig{AppsDomainAliases: []string{"old.example"}})
	if err != nil || d.Platform != "gregale.dev" || strings.Join(d.AppAliases, ",") != "old.example" {
		t.Fatalf("file fallback = %+v %v", d, err)
	}
}

func TestDeploySuffixFollowsConfiguredLayout(t *testing.T) {
	t.Setenv("FAAS_DEPLOY_DOMAIN", "")
	t.Setenv("FAAS_APPS_DOMAIN", "apps.env.example")
	if got := Configured().Apps; got != "apps.env.example" {
		t.Fatalf("unconfigured layout apps = %q, want the environment's", got)
	}
	restore := Configure(mustDomains(t, "gregale.dev", "gregale.app", "gregale.app", nil, ""))
	if got := DeploySuffix(); got != ".gregale.app" {
		t.Fatalf("DeploySuffix = %q", got)
	}
	inner := Configure(mustDomains(t, "", "apps.test.example", "", nil, ""))
	if got := DeploySuffix(); got != ".gregale.dev" {
		t.Fatalf("default DeploySuffix = %q, want the legacy deployment domain", got)
	}
	inner()
	if got := DeploySuffix(); got != ".gregale.app" {
		t.Fatalf("restored DeploySuffix = %q", got)
	}
	restore()
	if got := Configured().Apps; got != "apps.env.example" {
		t.Fatalf("restored layout apps = %q, want the environment's again", got)
	}
}
