package sealdomain_test

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/sealdomain"
)

func TestRegisterLoaded(t *testing.T) {
	for _, tc := range []struct {
		domain sealdomain.Domain
		loaded bool
		want   string
	}{
		{sealdomain.Runtime, true, `faas_seal_domain_loaded{domain="runtime"} 1`},
		{sealdomain.Source, false, `faas_seal_domain_loaded{domain="source"} 0`},
	} {
		reg := prometheus.NewRegistry()
		sealdomain.RegisterLoaded(reg, tc.domain, tc.loaded)
		want := `
# HELP faas_seal_domain_loaded 1 when this daemon loaded the seal domain's identity at start (ADR 20261022111812).
# TYPE faas_seal_domain_loaded gauge
` + tc.want + "\n"
		if err := testutil.GatherAndCompare(reg, strings.NewReader(want), "faas_seal_domain_loaded"); err != nil {
			t.Fatal(err)
		}
	}
}

// vmmd reads the runtime and build domains (ADR-20261026091500); a second
// RegisterLoaded call would register the gauge twice and panic.
func TestRegisterLoadedDomains(t *testing.T) {
	reg := prometheus.NewRegistry()
	sealdomain.RegisterLoadedDomains(reg, map[sealdomain.Domain]bool{sealdomain.Runtime: true, sealdomain.Build: false})
	want := `
# HELP faas_seal_domain_loaded 1 when this daemon loaded the seal domain's identity at start (ADR 20261022111812).
# TYPE faas_seal_domain_loaded gauge
faas_seal_domain_loaded{domain="build"} 0
faas_seal_domain_loaded{domain="runtime"} 1
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(want), "faas_seal_domain_loaded"); err != nil {
		t.Fatal(err)
	}
}
