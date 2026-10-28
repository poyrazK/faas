package edgerules

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/hostidentity"
)

// adr: 091 — amendment 2026-10-28 (review round 3): a credentialed CORS
// policy may not allow every subdomain of a domain the platform owns, since
// every other customer's app is served there.
func TestSharedDomainCORSOriginProblem(t *testing.T) {
	domains, err := hostidentity.NewDomains("platform.example", "apps.example", "deploy.example", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	defer hostidentity.Configure(domains)()
	for _, tc := range []struct {
		name    string
		origins []string
		creds   bool
		refused bool
	}{
		{name: "every app on the apps domain", origins: []string{"https://a.example", "https://*.apps.example"}, creds: true, refused: true},
		{name: "with a port", origins: []string{"https://*.apps.example:8443"}, creds: true, refused: true},
		{name: "every deployment host", origins: []string{"https://*.deploy.example"}, creds: true, refused: true},
		{name: "without credentials", origins: []string{"https://*.apps.example"}},
		{name: "the customer's own domain", origins: []string{"https://*.shop.example.com"}, creds: true},
		{name: "one app's origin", origins: []string{"https://shop.apps.example"}, creds: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := SharedDomainCORSOriginProblem(tc.origins, tc.creds) != nil; got != tc.refused {
				t.Fatalf("refused = %v, want %v", got, tc.refused)
			}
		})
	}
}
