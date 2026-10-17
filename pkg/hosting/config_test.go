package hosting

import "testing"

func TestHostingConfigurationIsExplicitAndIsolated(t *testing.T) {
	if config, err := Load(func(string) string { return "" }, "gregale.dev"); err != nil || config != nil {
		t.Fatalf("default config %+v %v", config, err)
	}
	if _, err := Load(func(key string) string {
		if key == "FAAS_FRONTEND_HOSTING_ENABLED" {
			return "1"
		}
		return ""
	}, "gregale.dev"); err == nil {
		t.Fatal("enabled unconfigured deployment accepted")
	}
	values := map[string]string{
		"FAAS_FRONTEND_HOSTING_DOMAIN":          "hosting.example",
		"FAAS_FRONTEND_HOSTING_R2_ENDPOINT":     "https://example.r2.cloudflarestorage.com",
		"FAAS_FRONTEND_HOSTING_R2_BUCKET":       "hosting",
		"FAAS_FRONTEND_HOSTING_R2_ACCESS_KEY":   "test-key",
		"FAAS_FRONTEND_HOSTING_R2_SECRET_KEY":   "test-secret",
		"FAAS_FRONTEND_HOSTING_CF_ACCOUNT_ID":   "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"FAAS_FRONTEND_HOSTING_KV_NAMESPACE_ID": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"FAAS_FRONTEND_HOSTING_CF_TOKEN":        "test-token",
	}
	getenv := func(key string) string { return values[key] }
	if config, err := Load(getenv, "apps.gregale.dev"); err != nil || config == nil {
		t.Fatalf("valid configuration: %+v %v", config, err)
	}
	values["FAAS_FRONTEND_HOSTING_ANALYTICS_ENABLED"] = "true"
	if _, err := Load(getenv, "apps.gregale.dev"); err == nil {
		t.Fatal("enabled unconfigured analytics accepted")
	}
	values["FAAS_FRONTEND_HOSTING_ANALYTICS_DATASET"] = "gregale_internal_pilot_traffic"
	values["FAAS_FRONTEND_HOSTING_ANALYTICS_CF_TOKEN"] = "read-token"
	if config, err := Load(getenv, "apps.gregale.dev"); err != nil || config.Analytics == nil {
		t.Fatalf("configured analytics: %v", err)
	}
	delete(values, "FAAS_FRONTEND_HOSTING_ANALYTICS_ENABLED")
	delete(values, "FAAS_FRONTEND_HOSTING_ANALYTICS_DATASET")
	delete(values, "FAAS_FRONTEND_HOSTING_ANALYTICS_CF_TOKEN")
	for _, domain := range []string{"sites.gregale.dev", "https://other.example", "other.example/path", "user@other.example", "other.example:443"} {
		values["FAAS_FRONTEND_HOSTING_DOMAIN"] = domain
		_, err := Load(getenv, "apps.gregale.dev")
		if err == nil {
			t.Fatalf("accepted unsafe hosting domain %q", domain)
		}
	}
}

func TestReleaseConfigOptInRequiresHostingProviders(t *testing.T) {
	// adr: 202610161909 — release policies are bounded and independently gated.
	for _, value := range []string{"1", "true"} {
		_, err := Load(func(key string) string {
			if key == "FAAS_FRONTEND_HOSTING_CONFIG_ENABLED" {
				return value
			}
			return ""
		}, "gregale.dev")
		if err == nil {
			t.Fatal("enabled release configuration without isolated hosting providers")
		}
	}
}

func TestPreviewAdmissionRequiresProviderConfiguration(t *testing.T) {
	for _, enabled := range []string{"true", "1"} {
		if _, err := Load(func(key string) string {
			if key == "FAAS_FRONTEND_HOSTING_PREVIEWS_ENABLED" {
				return enabled
			}
			return ""
		}, "gregale.dev"); err == nil {
			t.Fatal("enabled previews accepted missing isolated origin")
		}
	}
}

func TestHostingOIDCOptInRequiresIsolatedProviders(t *testing.T) {
	for _, enabled := range []string{"true", "1"} {
		if _, err := Load(func(key string) string {
			if key == "FAAS_FRONTEND_HOSTING_OIDC_ENABLED" {
				return enabled
			}
			return ""
		}, "gregale.dev"); err == nil {
			t.Fatal("OIDC enabled without isolated providers")
		}
	}
}

func TestHostingDiagnosticsNeedsIsolatedProviderConfiguration(t *testing.T) {
	for _, value := range []string{"1", "true"} {
		if _, err := Load(func(key string) string {
			if key == "FAAS_FRONTEND_HOSTING_DIAGNOSTICS_ENABLED" {
				return value
			}
			return ""
		}, "gregale.dev"); err == nil {
			t.Fatal("diagnostics without configured internal hosting")
		}
	}
}

func TestHostingCISetupNeedsIsolatedProviderConfiguration(t *testing.T) {
	for _, value := range []string{"1", "true"} {
		if _, err := Load(func(key string) string {
			if key == "FAAS_FRONTEND_HOSTING_CI_SETUP_ENABLED" {
				return value
			}
			return ""
		}, "gregale.dev"); err == nil {
			t.Fatal("setup without configured internal hosting")
		}
	}
}

func TestHostingActivityNeedsIsolatedProviderConfiguration(t *testing.T) {
	for _, value := range []string{"1", "true"} {
		if _, err := Load(func(key string) string {
			if key == "FAAS_FRONTEND_HOSTING_ACTIVITY_ENABLED" {
				return value
			}
			return ""
		}, "gregale.dev"); err == nil {
			t.Fatal("activity without internal hosting configuration")
		}
	}
}
