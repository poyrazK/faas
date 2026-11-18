// adr: 20261117185346 §4 — agentproxyd's flags: the tunnel, its slices,
// the agent addresses, and a registration listener inside the tunnel.
package main

import (
	"strings"
	"testing"
)

func TestParseFlags(t *testing.T) {
	good := []string{"-tunnel-cidr", "10.78.0.0/16", "-addresses", "198.51.100.7, 198.51.100.8", "-registration-listen", "10.78.0.1:8091"}
	cfg, err := parseFlags(good)
	if err != nil || len(cfg.addresses) != 2 || cfg.nodePrefix != 22 || cfg.httpListen != "127.0.0.1:15080" {
		t.Fatalf("cfg = %+v, %v", cfg, err)
	}
	for _, tc := range []struct{ name, flag, value string }{
		{"v6 tunnel", "-tunnel-cidr", "fd00::/64"},
		{"prefix", "-node-prefix", "16"},
		{"address", "-addresses", "198.51.100.7,nope"},
		{"registration outside the tunnel", "-registration-listen", "0.0.0.0:8091"},
	} {
		args := append([]string{}, good...)
		args = append(args, tc.flag, tc.value)
		if _, err := parseFlags(args); err == nil || !strings.Contains(err.Error(), "agentproxyd") {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
}
