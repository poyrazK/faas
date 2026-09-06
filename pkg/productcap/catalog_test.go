package productcap

import (
	"strings"
	"testing"
)

func TestLoadCatalog(t *testing.T) {
	catalog, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if catalog.Version != 1 {
		t.Fatalf("version = %d, want 1", catalog.Version)
	}
	if len(catalog.Capabilities) < 10 {
		t.Fatalf("capability count = %d, want at least 10", len(catalog.Capabilities))
	}
}

func TestValidateRejectsUnsortedIDs(t *testing.T) {
	catalog := Catalog{
		Version: 1,
		Capabilities: []Capability{
			{ID: "z-last", Name: "Z", Category: "runtime", Description: "z", Maturity: MaturityBeta, Plans: []string{"free"}, DocsURL: "/docs/z", AcceptanceTest: "pkg/test::TestZ"},
			{ID: "a-first", Name: "A", Category: "runtime", Description: "a", Maturity: MaturityBeta, Plans: []string{"free"}, DocsURL: "/docs/a", AcceptanceTest: "pkg/test::TestA"},
		},
	}
	if err := Validate(catalog); err == nil || !strings.Contains(err.Error(), "sorted") {
		t.Fatalf("Validate error = %v, want sorted error", err)
	}
}

func TestValidateRejectsPublicCapabilityWithoutDocs(t *testing.T) {
	catalog := Catalog{
		Version: 1,
		Capabilities: []Capability{
			{ID: "api", Name: "API", Category: "runtime", Description: "api", Maturity: MaturityGA, Plans: []string{"free"}, AcceptanceTest: "pkg/test::TestAPI"},
		},
	}
	if err := Validate(catalog); err == nil || !strings.Contains(err.Error(), "needs a /docs URL") {
		t.Fatalf("Validate error = %v, want docs error", err)
	}
}

func TestJSONReturnsCopy(t *testing.T) {
	first := JSON()
	first[0] = 'X'
	second := JSON()
	if second[0] == 'X' {
		t.Fatal("JSON returned the embedded backing slice")
	}
}
