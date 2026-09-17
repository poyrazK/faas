package e2etest

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Every metrics-address stamp the harness writes must be a per-test free
// port. A fixed or empty value is the shape that let apid die on a held
// 127.0.0.1:9101 in smoke run 35217250297.
func TestHarness_EveryMetricsAddrIsAFreePort(t *testing.T) {
	src, err := os.ReadFile("harness.go")
	if err != nil {
		t.Fatal(err)
	}
	stamps := regexp.MustCompile(`"FAAS_\w+_METRICS_ADDR=("?)[^,\n]*`).FindAllString(string(src), -1)
	if len(stamps) < 2 {
		t.Fatalf("expected apid and imaged metrics stamps in harness.go, found %d: %q", len(stamps), stamps)
	}
	for _, s := range stamps {
		if !strings.Contains(s, "metricsAddrFor(") {
			t.Errorf("%s does not use a free per-test port; a daemon that cannot bind its "+
				"metrics port exits before serving anything", s)
		}
	}
	if !strings.Contains(string(src), "metrics_addr = %q") {
		t.Error("builderd.toml written by the harness has no metrics_addr; builderd would bind its fixed default 127.0.0.1:9105")
	}
}

func TestBuilderdConfig_CarriesMetricsAddr(t *testing.T) {
	cfg := builderdConfig(t.TempDir(), "/run/x.sock", "/srv/base.ext4", "127.0.0.1:45678")
	if !strings.Contains(cfg, `metrics_addr = "127.0.0.1:45678"`) {
		t.Fatalf("builderd config lacks the metrics address:\n%s", cfg)
	}
}

func TestMetricsAddrFor_IsLoopbackAndDistinct(t *testing.T) {
	a, b := metricsAddrFor(t, "apid"), metricsAddrFor(t, "imaged")
	for _, addr := range []string{a, b} {
		if !strings.HasPrefix(addr, "127.0.0.1:") || strings.HasSuffix(addr, ":0") {
			t.Errorf("metricsAddrFor returned %q, want a concrete loopback port", addr)
		}
	}
	if a == b {
		t.Errorf("two daemons got the same metrics address %q", a)
	}
}
