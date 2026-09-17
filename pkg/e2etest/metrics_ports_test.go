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
	lines := strings.Split(string(src), "\n")
	starts := regexp.MustCompile(`startProc\(t, bin, "(apid|imaged)", env\)`)
	found := 0
	for i, line := range lines {
		m := starts.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		found++
		// The env for this start is assembled in the lines just above it;
		// one of them must stamp a free port for this daemon. #2881 fixed
		// one of two apid start sites and missed the one Start actually
		// uses (smoke run 35220096082).
		stamp := `"FAAS_` + strings.ToUpper(m[1]) + `_METRICS_ADDR="+metricsAddrFor(t, "` + m[1] + `")`
		window := strings.Join(lines[max(0, i-80):i], "\n")
		// imaged's env is assembled by imagedEnv; the stamp lives there.
		if helper := m[1] + "Env("; strings.Contains(window, helper) {
			if body := funcBody(string(src), "func "+helper); strings.Contains(body, stamp) {
				continue
			}
		}
		if !strings.Contains(window, stamp) {
			t.Errorf("harness.go:%d starts %s without %s in the 80 lines above it; that daemon "+
				"would bind its fixed default metrics port and exit on a collision", i+1, m[1], stamp)
		}
	}
	if found < 3 {
		t.Fatalf("expected at least two apid start sites and one imaged in harness.go, found %d", found)
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

// funcBody returns the source from the start of the named function to the
// next top-level declaration, or "" if the function is absent.
func funcBody(src, header string) string {
	i := strings.Index(src, header)
	if i < 0 {
		return ""
	}
	rest := src[i+len(header):]
	if j := strings.Index(rest, "\n}\n"); j >= 0 {
		rest = rest[:j]
	}
	return rest
}
