// Tests for pkg/billing/loader/config.go — LoadBillingConfig +
// ApplyBillingEnvOverlay. Uses inline-string TOML fixtures (no
// testdata/*.toml) per cmd/schedd/config_test.go:42-73 precedent.
// Each test is a tripwire: a regression in tag names, env-overlay
// precedence, or post-fill behaviour breaks the named assertion.
package loader

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/billing/paddle"
	"github.com/onebox-faas/faas/pkg/billing/polar"
	"github.com/onebox-faas/faas/pkg/billing/stripe"
)

func TestLoadBillingConfig_NilBodyReturnsDefaults(t *testing.T) {
	// nil body (matching the missing-file case via LoadBillingConfigFromPath)
	// returns non-nil cfg with both sub-configs populated + Defaults applied.
	cfg, err := LoadBillingConfig(nil)
	if err != nil {
		t.Fatalf("LoadBillingConfig nil body: %v", err)
	}
	if cfg == nil {
		t.Fatal("cfg is nil")
	}
	if cfg.Stripe == nil {
		t.Fatal("cfg.Stripe is nil after LoadBillingConfig")
	}
	if cfg.Paddle == nil {
		t.Fatal("cfg.Paddle is nil after LoadBillingConfig")
	}
	if cfg.Polar == nil {
		t.Fatal("cfg.Polar is nil after LoadBillingConfig")
	}
	// Stripe Defaults() applies the 5-minute tolerance.
	if cfg.Stripe.ToleranceSeconds != 300 {
		t.Errorf("cfg.Stripe.ToleranceSeconds = %d, want 300 (Defaults applied)", cfg.Stripe.ToleranceSeconds)
	}
	// Paddle Defaults() is a no-op today; Sandbox must be false.
	if cfg.Paddle.Sandbox {
		t.Error("cfg.Paddle.Sandbox = true, want false")
	}
	// Provider is empty by default — loader translates to "stripe".
	if cfg.Provider != "" {
		t.Errorf("cfg.Provider = %q, want \"\"", cfg.Provider)
	}
}

func TestLoadBillingConfig_MissingFileReturnsDefaults(t *testing.T) {
	// /nonexistent.toml — non-fatal via LoadBillingConfigFromPath.
	cfg, err := LoadBillingConfigFromPath(filepath.Join(t.TempDir(), "does-not-exist.toml"))
	if err != nil {
		t.Fatalf("LoadBillingConfigFromPath missing file: %v", err)
	}
	if cfg == nil || cfg.Stripe == nil || cfg.Paddle == nil || cfg.Polar == nil {
		t.Fatalf("expected non-nil cfg with both sub-configs, got %+v", cfg)
	}
	if cfg.Stripe.ToleranceSeconds != 300 {
		t.Errorf("cfg.Stripe.ToleranceSeconds = %d, want 300", cfg.Stripe.ToleranceSeconds)
	}
}

func TestLoadBillingConfig_NoBillingHeaderInBodyReturnsDefaults(t *testing.T) {
	// File has daemon-level fields but no [billing] header — must
	// return defaults (not an error). Mirrors the prod case where
	// apid.toml has the daemon's own keys but no billing block.
	body := []byte(`
listen_addr = "tcp://0.0.0.0:8080"
some_other_field = 42
`)
	cfg, err := LoadBillingConfig(body)
	if err != nil {
		t.Fatalf("LoadBillingConfig no-header: %v", err)
	}
	if cfg == nil {
		t.Fatal("cfg is nil")
	}
	if cfg.Stripe == nil {
		t.Fatal("cfg.Stripe is nil")
	}
	if cfg.Paddle == nil {
		t.Fatal("cfg.Paddle is nil")
	}
}

func TestLoadBillingConfig_ParsesStripeBlock(t *testing.T) {
	body := []byte(`
[billing]
provider = "stripe"

[billing.stripe]
api_key = "sk_test_x"
webhook_secret = "whsec_x"
tolerance_seconds = 600
`)
	cfg, err := LoadBillingConfig(body)
	if err != nil {
		t.Fatalf("LoadBillingConfig: %v", err)
	}
	if cfg.Provider != "stripe" {
		t.Errorf("cfg.Provider = %q, want \"stripe\"", cfg.Provider)
	}
	if cfg.Stripe == nil {
		t.Fatal("cfg.Stripe is nil")
	}
	// Tag typo (e.g. api_key → apikey) breaks the next assertion
	// because cfg.Stripe.APIKey stays "".
	if cfg.Stripe.APIKey != "sk_test_x" {
		t.Errorf("cfg.Stripe.APIKey = %q, want \"sk_test_x\"", cfg.Stripe.APIKey)
	}
	if cfg.Stripe.WebhookSecret != "whsec_x" {
		t.Errorf("cfg.Stripe.WebhookSecret = %q, want \"whsec_x\"", cfg.Stripe.WebhookSecret)
	}
	// Defaults() must NOT overwrite a value explicitly set in TOML.
	if cfg.Stripe.ToleranceSeconds != 600 {
		t.Errorf("cfg.Stripe.ToleranceSeconds = %d, want 600 (not overwritten by Defaults)", cfg.Stripe.ToleranceSeconds)
	}
}

func TestLoadBillingConfig_ParsesPaddleBlock(t *testing.T) {
	body := []byte(`
[billing]
provider = "paddle"

[billing.paddle]
api_key = "pdl_x"
webhook_secret = "whk_x"
sandbox = true
`)
	cfg, err := LoadBillingConfig(body)
	if err != nil {
		t.Fatalf("LoadBillingConfig: %v", err)
	}
	if cfg.Provider != "paddle" {
		t.Errorf("cfg.Provider = %q, want \"paddle\"", cfg.Provider)
	}
	if cfg.Paddle == nil {
		t.Fatal("cfg.Paddle is nil")
	}
	// Tag typo (e.g. api_key → apikey) breaks the next assertion.
	if cfg.Paddle.APIKey != "pdl_x" {
		t.Errorf("cfg.Paddle.APIKey = %q, want \"pdl_x\"", cfg.Paddle.APIKey)
	}
	if cfg.Paddle.WebhookSecret != "whk_x" {
		t.Errorf("cfg.Paddle.WebhookSecret = %q, want \"whk_x\"", cfg.Paddle.WebhookSecret)
	}
	if !cfg.Paddle.Sandbox {
		t.Error("cfg.Paddle.Sandbox = false, want true")
	}
}

func TestLoadBillingConfig_ParsesPolarBlock(t *testing.T) {
	body := []byte(`
[billing]
provider = "polar"

[billing.polar]
api_key = "polar_token"
webhook_secret = "whsec_secret"
sandbox = true
hobby_product_id = "hobby-product"
pro_product_id = "pro-product"
scale_product_id = "scale-product"
usage_event_name = "ram_usage"
`)
	cfg, err := LoadBillingConfig(body)
	if err != nil {
		t.Fatalf("LoadBillingConfig: %v", err)
	}
	if cfg.Provider != "polar" || cfg.Polar == nil {
		t.Fatalf("polar config not loaded: provider=%q cfg=%+v", cfg.Provider, cfg.Polar)
	}
	if cfg.Polar.APIKey != "polar_token" || cfg.Polar.WebhookSecret != "whsec_secret" {
		t.Fatalf("polar credentials not loaded: %+v", cfg.Polar)
	}
	if !cfg.Polar.Sandbox || cfg.Polar.UsageEventName != "ram_usage" {
		t.Fatalf("polar settings not loaded: %+v", cfg.Polar)
	}
}

func TestLoadBillingConfig_PartialTOMLFillsDefaults(t *testing.T) {
	// Only [billing.stripe] — Paddle is absent but must still be
	// non-nil post-Load (with Defaults() applied) so downstream
	// code can dereference cfg.Paddle without a nil check.
	body := []byte(`
[billing.stripe]
api_key = "sk_test_y"
`)
	cfg, err := LoadBillingConfig(body)
	if err != nil {
		t.Fatalf("LoadBillingConfig: %v", err)
	}
	if cfg.Stripe == nil {
		t.Fatal("cfg.Stripe is nil")
	}
	if cfg.Paddle == nil {
		t.Fatal("cfg.Paddle is nil — loader must post-fill absent tables")
	}
	if cfg.Stripe.APIKey != "sk_test_y" {
		t.Errorf("cfg.Stripe.APIKey = %q, want \"sk_test_y\"", cfg.Stripe.APIKey)
	}
	// Stripe Defaults() applied even though tolerance was absent.
	if cfg.Stripe.ToleranceSeconds != 300 {
		t.Errorf("cfg.Stripe.ToleranceSeconds = %d, want 300", cfg.Stripe.ToleranceSeconds)
	}
	// Paddle Defaults() is a no-op; Sandbox stays false.
	if cfg.Paddle.Sandbox {
		t.Error("cfg.Paddle.Sandbox = true, want false")
	}
}

func TestLoadBillingConfig_BadTOMLReturnsError(t *testing.T) {
	body := []byte("this is = not = valid toml =")
	if _, err := LoadBillingConfig(body); err == nil {
		t.Fatal("LoadBillingConfig bad TOML: err = nil, want error")
	}
}

func TestLoadBillingConfigFromPath_RealFile(t *testing.T) {
	// Sanity-check the file-path entry point with a real temp file.
	path := filepath.Join(t.TempDir(), "apid.toml")
	if err := os.WriteFile(path, []byte(`
[billing]
provider = "stripe"

[billing.stripe]
api_key = "sk_file"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadBillingConfigFromPath(path)
	if err != nil {
		t.Fatalf("LoadBillingConfigFromPath: %v", err)
	}
	if cfg.Stripe.APIKey != "sk_file" {
		t.Errorf("cfg.Stripe.APIKey = %q, want \"sk_file\"", cfg.Stripe.APIKey)
	}
}

func TestApplyBillingEnvOverlay_EnvWinsOverTOML(t *testing.T) {
	// TOML has stripe.api_key = "toml_key", env has STRIPE_API_KEY="env_key".
	// Post-overlay must read "env_key" — env wins.
	env := func(k string) string {
		switch k {
		case "STRIPE_API_KEY":
			return "env_key"
		}
		return ""
	}
	cfg := &RootBillingConfig{
		Provider: "stripe",
		Stripe:   &stripe.Config{APIKey: "toml_key"},
		Paddle:   &paddle.Config{},
	}
	cfg = ApplyBillingEnvOverlay(cfg, env)
	if cfg.Stripe.APIKey != "env_key" {
		t.Errorf("cfg.Stripe.APIKey = %q, want \"env_key\" (env wins over TOML)", cfg.Stripe.APIKey)
	}
}

func TestApplyBillingEnvOverlay_EmptyEnvLeavesTOML(t *testing.T) {
	// Empty env → TOML value is preserved (no overwrite).
	cfg := &RootBillingConfig{
		Provider: "stripe",
		Stripe:   &stripe.Config{APIKey: "toml_key", WebhookSecret: "whsec_toml", ToleranceSeconds: 300},
		Paddle:   &paddle.Config{},
	}
	cfg = ApplyBillingEnvOverlay(cfg, func(string) string { return "" })
	if cfg.Stripe.APIKey != "toml_key" {
		t.Errorf("cfg.Stripe.APIKey = %q, want \"toml_key\" (no env override)", cfg.Stripe.APIKey)
	}
	if cfg.Stripe.WebhookSecret != "whsec_toml" {
		t.Errorf("cfg.Stripe.WebhookSecret = %q, want \"whsec_toml\"", cfg.Stripe.WebhookSecret)
	}
}

func TestApplyBillingEnvOverlay_FAAS_PADDLE_SANDBOX_AcceptsTrueVariants(t *testing.T) {
	// FAAS_PADDLE_SANDBOX accepts both "1" and "true". Dropping either
	// branch breaks the corresponding case below.
	cases := []struct {
		v    string
		want bool
	}{
		{"1", true},
		{"true", true},
		{"0", false},
		{"false", false},
		{"", false}, // empty env leaves TOML value (false default)
	}
	for _, c := range cases {
		env := func(k string) string {
			if k == "FAAS_PADDLE_SANDBOX" {
				return c.v
			}
			return ""
		}
		cfg := &RootBillingConfig{
			Stripe: &stripe.Config{},
			Paddle: &paddle.Config{},
			Polar:  &polar.Config{},
		}
		cfg = ApplyBillingEnvOverlay(cfg, env)
		if cfg.Paddle.Sandbox != c.want {
			t.Errorf("FAAS_PADDLE_SANDBOX=%q: cfg.Paddle.Sandbox = %v, want %v", c.v, cfg.Paddle.Sandbox, c.want)
		}
	}
}

// TestApplyBillingEnvOverlay_FAAS_PADDLE_WEBHOOK_TOLERANCE_SECONDS (PR-P4).
// Validates that the operator knob propagates into cfg.Paddle.ToleranceSeconds,
// that a bad parse (non-integer) is silently dropped (matching the
// "stale TOML is safer than a noisy Warn" rationale), and that env wins
// over TOML (matching every other ApplyBillingEnvOverlay case).
func TestApplyBillingEnvOverlay_FAAS_PADDLE_WEBHOOK_TOLERANCE_SECONDS(t *testing.T) {
	cases := []struct {
		name    string
		envVal  string
		tomlVal int
		want    int
	}{
		{name: "env overrides TOML", envVal: "120", tomlVal: 300, want: 120},
		{name: "env alone", envVal: "60", tomlVal: 0, want: 60},
		{name: "TOML alone", envVal: "", tomlVal: 600, want: 600},
		{name: "neither", envVal: "", tomlVal: 0, want: 0},
		{name: "bad parse silently dropped", envVal: "not-a-number", tomlVal: 300, want: 300},
		{name: "zero is preserved (operator opted out)", envVal: "0", tomlVal: 300, want: 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := func(k string) string {
				if k == "FAAS_PADDLE_WEBHOOK_TOLERANCE_SECONDS" {
					return c.envVal
				}
				return ""
			}
			cfg := &RootBillingConfig{
				Stripe: &stripe.Config{},
				Paddle: &paddle.Config{ToleranceSeconds: c.tomlVal},
				Polar:  &polar.Config{},
			}
			cfg = ApplyBillingEnvOverlay(cfg, env)
			if cfg.Paddle.ToleranceSeconds != c.want {
				t.Errorf("got ToleranceSeconds=%d, want %d (env=%q, toml=%d)",
					cfg.Paddle.ToleranceSeconds, c.want, c.envVal, c.tomlVal)
			}
		})
	}
}

func TestApplyBillingEnvOverlay_FAAS_BILLING_PROVIDER_OverridesTOML(t *testing.T) {
	// TOML provider = "stripe" + env FAAS_BILLING_PROVIDER="paddle" →
	// cfg.Provider must be "paddle" (env wins).
	env := func(k string) string {
		if k == "FAAS_BILLING_PROVIDER" {
			return "paddle"
		}
		return ""
	}
	cfg := &RootBillingConfig{
		Provider: "stripe",
		Stripe:   &stripe.Config{},
		Paddle:   &paddle.Config{},
		Polar:    &polar.Config{},
	}
	cfg = ApplyBillingEnvOverlay(cfg, env)
	if cfg.Provider != "paddle" {
		t.Errorf("cfg.Provider = %q, want \"paddle\" (env wins over TOML)", cfg.Provider)
	}
}

func TestApplyBillingEnvOverlay_NilCfgReturnsNil(t *testing.T) {
	// Defensive: a nil cfg returns nil (no panic). The cmd/{apid,
	// meterd} caller-side guards already handle nil, but the loader
	// helper must not panic on a nil input.
	if got := ApplyBillingEnvOverlay(nil, func(string) string { return "" }); got != nil {
		t.Errorf("ApplyBillingEnvOverlay(nil) = %v, want nil", got)
	}
}

func TestApplyBillingEnvOverlay_NilStripeSubConfigFilled(t *testing.T) {
	// Defensive: if cfg.Stripe is nil (caller skipped LoadBillingConfig
	// or pushed a hand-rolled cfg), the overlay must not panic.
	cfg := &RootBillingConfig{
		Paddle: &paddle.Config{},
	}
	cfg = ApplyBillingEnvOverlay(cfg, func(string) string { return "x" })
	if cfg.Stripe == nil {
		t.Fatal("cfg.Stripe is nil after overlay")
	}
	if cfg.Stripe.APIKey != "x" {
		t.Errorf("cfg.Stripe.APIKey = %q, want \"x\"", cfg.Stripe.APIKey)
	}
}

// TestResolveSecret_EnvWinsOverTOML is the unit-level tripwire for
// the closure-side precedence. Without resolveSecret, the closures
// only read env and the TOML values are silently ignored. The pure
// function test fails if a future refactor swaps the order or drops
// the helper.
func TestResolveSecret_EnvWinsOverTOML(t *testing.T) {
	cases := []struct {
		envVal, tomlVal, want string
	}{
		{"env_key", "toml_key", "env_key"}, // env wins
		{"", "toml_key", "toml_key"},       // empty env falls through
		{"env_key", "", "env_key"},         // empty TOML, env kept
		{"", "", ""},                       // both empty
	}
	for _, c := range cases {
		got := resolveSecret(c.envVal, c.tomlVal)
		if got != c.want {
			t.Errorf("resolveSecret(%q, %q) = %q, want %q", c.envVal, c.tomlVal, got, c.want)
		}
	}
}

// TestResolveSandbox_EnvAndTOMLPrecedence pins the FAAS_PADDLE_SANDBOX
// precedence at the closure side. The unit-level tripwire lets a
// regression in the helper surface independently of the closures.
func TestResolveSandbox_EnvAndTOMLPrecedence(t *testing.T) {
	cases := []struct {
		envVal string
		toml   bool
		want   bool
	}{
		{"1", false, true},     // "1" overrides TOML=false
		{"true", false, true},  // "true" overrides TOML=false
		{"0", true, false},     // "0" overrides TOML=true
		{"false", true, false}, // "false" overrides TOML=true
		{"", true, true},       // empty env keeps TOML=true
		{"", false, false},     // empty env keeps TOML=false
		{"weird", true, true},  // unknown env value falls through to TOML
	}
	for _, c := range cases {
		got := resolveSandbox(c.envVal, c.toml)
		if got != c.want {
			t.Errorf("resolveSandbox(%q, %v) = %v, want %v", c.envVal, c.toml, got, c.want)
		}
	}
}
