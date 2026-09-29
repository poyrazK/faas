// adr: 375
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
