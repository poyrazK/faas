// adr: 570
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/gateway"
)

func TestLoadConfigTrafficCounterMode(t *testing.T) {
	for _, test := range []struct {
		name, input, want string
		invalid           bool
	}{
		{"missing", "", "central", false},
		{"central", "central", "central", false},
		{"explicit local", "local", "local", false},
		{"unknown", "nonsense", "", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "gatewayd.toml")
			if test.input != "" {
				if err := os.WriteFile(path, []byte("[ratelimit]\nmode = \""+test.input+"\"\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			cfg, err := LoadConfig(path)
			if test.invalid {
				if err == nil || !strings.Contains(err.Error(), "ratelimit.mode") {
					t.Fatalf("invalid mode: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.RateLimit.Mode != test.want {
				t.Fatalf("mode=%q want=%q", cfg.RateLimit.Mode, test.want)
			}
		})
	}
	if cfg, err := LoadConfig(""); err != nil || cfg.RateLimit.Mode != "central" {
		t.Fatalf("env-only default=(%v,%v)", cfg, err)
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
		{name: "central default", toml: "[ratelimit]\nmode = \"central\"\n"},
		{name: "local mode without lease", toml: "[ratelimit]\nmode = \"local\"\n"},
		{name: "central with tunables", toml: "[ratelimit]\nmode = \"central\"\nlease = true\nlease_window_ms = 100\nlease_fraction = 0.2\nallowance_fraction = 0.01\n",
			want: gateway.LeaseConfig{Window: 100 * time.Millisecond, Fraction: 0.2, AllowanceFraction: 0.01}},
		// ADR-570: an unset mode is central, so local mode must be explicit.
		{name: "local mode", toml: "[ratelimit]\nmode = \"local\"\nlease = true\n", wantErr: "ratelimit.lease requires"},
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
	// Leasing is on unless turned off, and only in central mode (H39).
	for toml, want := range map[string]bool{
		"[ratelimit]\nmode = \"central\"\n":                true,
		"[ratelimit]\nmode = \"central\"\nlease = false\n": false,
		"[ratelimit]\nmode = \"local\"\n":                  false,
	} {
		path := filepath.Join(t.TempDir(), "gatewayd.toml")
		if err := os.WriteFile(path, []byte(toml), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadConfig(path)
		if err != nil {
			t.Fatalf("LoadConfig: %v", err)
		}
		if got := cfg.RateLimit.leaseEnabled(); got != want {
			t.Errorf("%q: leaseEnabled = %v, want %v", toml, got, want)
		}
	}
}
