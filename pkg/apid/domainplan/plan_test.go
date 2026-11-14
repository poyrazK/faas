package domainplan_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/domainplan"
)

func TestDomainManifestNormalization(t *testing.T) {
	input := api.DomainManifest{Version: 1, Domains: []api.DomainBindingSpec{{Domain: " B.EXAMPLE.COM ", App: "b"}, {Domain: "a.example.com", App: "a", Environment: "stage"}}}
	out, err := domainplan.Normalize(input)
	if err != nil || out.Domains[0].Domain != "a.example.com" || out.Domains[1].Domain != "b.example.com" {
		t.Fatalf("normalize=%+v, %v", out, err)
	}
	if input.Domains[0].Domain != " B.EXAMPLE.COM " {
		t.Fatal("normalization changed the caller's manifest")
	}
	input.Domains[1].Domain = "b.example.com"
	if _, err := domainplan.Normalize(input); err == nil {
		t.Fatal("duplicate normalized domains accepted")
	}
}

func TestDomainPlanFingerprintFencesPrivateIdentity(t *testing.T) {
	p := api.DomainPlan{Version: 1, Manifest: api.DomainManifest{Version: 1, Domains: []api.DomainBindingSpec{{Domain: "a.example.com", App: "a"}}}}
	a, err := domainplan.Fingerprint(p, "old-proof")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := domainplan.Fingerprint(p, "replacement-proof")
	if a == b || !domainplan.ValidDigest(a) || strings.Contains(a, "proof") {
		t.Fatal("claim identity was not privately fenced")
	}
}
