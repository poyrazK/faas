package toolregistry

import (
	"fmt"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

func TestPrivatePublicationCombinedEvidenceBudget(t *testing.T) {
	p := api.ToolPublicationRecord{Intent: api.ToolPublicationIntent{
		App: "budget", SourceSHA256: strings.Repeat("1", 64), FixturesSHA256: strings.Repeat("2", 64),
		PlatformVerification: true, AccessMode: "external-oauth", EndpointPath: "/mcp",
		Auth:       &api.ToolPublicationAuth{Issuer: "https://issuer.example", JWKSURL: "https://issuer.example/jwks", Resource: "https://budget.example/mcp", Scopes: []string{}},
		ToolScopes: map[string][]string{"check": {}}, TestCases: []string{"one"},
	}}
	catalog := []byte(strings.Repeat("x", api.ToolPublicationCatalogMaxBytes))
	if !publicationVerificationFits(p, api.ToolPublicationVerification{}, catalog) {
		t.Fatal("ordinary review must retain a maximum-size catalog")
	}
	p.Intent.TestCases = nil
	for i := range api.ToolPublicationMaxCases {
		p.Intent.TestCases = append(p.Intent.TestCases, strings.Repeat("\x01", 125)+fmt.Sprintf("%03d", i))
	}
	if err := toolpublication.ValidateIntent(p.Intent); err != nil {
		t.Fatalf("review must be valid independently of combined evidence size: %v", err)
	}
	if publicationVerificationFits(p, api.ToolPublicationVerification{}, catalog) {
		t.Fatal("escaped names and duplicate evidence exceed the native journal ceiling")
	}
}
