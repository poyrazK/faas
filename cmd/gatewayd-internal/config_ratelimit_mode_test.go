// Pin the [ratelimit] TOML knob onto gatewayd-internal's Config
// (ADR-104 amendment 5, issue #881 Phase 4 C2). Mirrors the
// node_name_env test shape — TOML round-trip + default + missing
// file shape — so a future config refactor that drops or renames
// the field surfaces here, not at the daemon boot.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/gateway"
)

func TestLoadConfig_RateLimitModeDefault(t *testing.T) {
	// Missing file → RateLimit.Mode is "local" (the back-compat
	// default). Single-box dev MUST NOT require a TOML entry to
	// start the daemon.
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.RateLimit.Mode != "local" {
		t.Errorf("default RateLimit.Mode = %q, want local (back-compat default)", cfg.RateLimit.Mode)
	}
}

func TestLoadConfig_RateLimitModeCentral(t *testing.T) {
	// TOML sets mode = "central"; LoadConfig must round-trip the
	// value unchanged. Wire-time validation (rejecting unknown
	// values) lives in run.go's startup gate — the config package
	// only knows about the on-disk shape.
	path := filepath.Join(t.TempDir(), "gatewayd.toml")
	if err := os.WriteFile(path, []byte("[ratelimit]\nmode = \"central\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.RateLimit.Mode != "central" {
		t.Errorf("RateLimit.Mode = %q, want central (TOML round-trip)", cfg.RateLimit.Mode)
	}
}

func TestLoadConfig_RateLimitModeUnknownAccepted(t *testing.T) {
	// The config layer accepts any string; run.go validates the
	// closed vocabulary and rejects unknown values at boot. This
	// pins the shape — a future move that adds a strict enum
	// validator here would surface here, not at startup.
	path := filepath.Join(t.TempDir(), "gatewayd.toml")
	if err := os.WriteFile(path, []byte("[ratelimit]\nmode = \"nonsense\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !strings.Contains(cfg.RateLimit.Mode, "nonsense") {
		t.Errorf("RateLimit.Mode = %q, want nonsense preserved verbatim (boot gate validates, not config)", cfg.RateLimit.Mode)
	}
}

// TestRateLimitLeaseConfig pins the ADR-1520 knobs: leasing needs central
// mode, zero takes the defaults, and out-of-range tunables are refused at
// boot with their TOML key.
func TestRateLimitLeaseConfig(t *testing.T) {
	for _, tc := range []struct {
		name    string
		toml    string
		wantErr string
		want    gateway.LeaseConfig
	}{
		{name: "off by default", toml: "[ratelimit]\nmode = \"central\"\n"},
		{name: "central with tunables", toml: "[ratelimit]\nmode = \"central\"\nlease = true\nlease_window_ms = 100\nlease_fraction = 0.2\nallowance_fraction = 0.01\n",
			want: gateway.LeaseConfig{Window: 100 * time.Millisecond, Fraction: 0.2, AllowanceFraction: 0.01}},
		{name: "local mode", toml: "[ratelimit]\nlease = true\n", wantErr: "ratelimit.lease requires"},
		{name: "window too long", toml: "[ratelimit]\nmode = \"central\"\nlease_window_ms = 5000\n", wantErr: "ratelimit.lease_window_ms"},
		{name: "fraction above one", toml: "[ratelimit]\nmode = \"central\"\nlease_fraction = 1.5\n", wantErr: "ratelimit.lease_fraction"},
		{name: "negative allowance", toml: "[ratelimit]\nmode = \"central\"\nallowance_fraction = -0.1\n", wantErr: "ratelimit.allowance_fraction"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "gatewayd.toml")
			if err := os.WriteFile(path, []byte(tc.toml), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadConfig(path)
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}
			got, err := cfg.RateLimit.leaseConfig()
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("leaseConfig error = %v, want one naming %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("leaseConfig = %+v, %v; want %+v", got, err, tc.want)
			}
		})
	}
}
