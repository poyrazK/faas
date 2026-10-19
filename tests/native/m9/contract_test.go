// spec: §14.A
// adr: 1530
package m9

import (
	"encoding/json"
	"strings"
	"testing"
)

func fixtureConfig() Config {
	return Config{Version: 1, AppID: "11111111-1111-4111-8111-111111111111",
		DeploymentID: "22222222-2222-4222-8222-222222222222", FixtureID: "33333333-3333-4333-8333-333333333333",
		TimeoutSeconds: 180,
		Source:         Peer{Name: "source", SSH: "faas@source", Socket: "/run/faas/vmmd.sock", VMMDSHA256: strings.Repeat("a", 64)},
		Destination:    Peer{Name: "destination", SSH: "faas@destination", Socket: "/run/faas/vmmd.sock", VMMDSHA256: strings.Repeat("a", 64)}}
}

func TestConfigRejectsUnsafeFaultTargets(t *testing.T) {
	for name, change := range map[string]func(*Config){
		"version":          func(c *Config) { c.Version = 2 },
		"nil fixture":      func(c *Config) { c.FixtureID = "00000000-0000-0000-0000-000000000000" },
		"short budget":     func(c *Config) { c.TimeoutSeconds = 1 },
		"same target":      func(c *Config) { c.Destination.SSH = c.Source.SSH },
		"same node":        func(c *Config) { c.Destination.Name = c.Source.Name },
		"ssh option":       func(c *Config) { c.Source.SSH = "-oProxyCommand=id" },
		"ssh shell":        func(c *Config) { c.Source.SSH = "source;id" },
		"socket escape":    func(c *Config) { c.Source.Socket = "/run/faas/../other.sock" },
		"socket delimiter": func(c *Config) { c.Source.Socket = "/run/faas/vmmd:other.sock" },
		"mixed binary":     func(c *Config) { c.Destination.VMMDSHA256 = strings.Repeat("b", 64) },
	} {
		t.Run(name, func(t *testing.T) {
			c := fixtureConfig()
			change(&c)
			raw, err := json.Marshal(c)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeConfig(strings.NewReader(string(raw))); err == nil {
				t.Fatal("unsafe native fault target accepted")
			}
		})
	}
}

func TestConfigDecodeBoundsAndDefaults(t *testing.T) {
	c := fixtureConfig()
	c.TimeoutSeconds = 0
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeConfig(strings.NewReader(string(raw)))
	if err != nil || got.TimeoutSeconds != 180 {
		t.Fatalf("default budget: %v, %v", got, err)
	}
	for _, invalid := range []string{string(raw) + "{}", strings.Repeat(" ", 65537),
		strings.TrimSuffix(string(raw), "}") + `,"surprise":true}`} {
		if _, err := DecodeConfig(strings.NewReader(invalid)); err == nil {
			t.Fatal("invalid configuration accepted")
		}
	}
}
