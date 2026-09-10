package apihostingscorecard

import (
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/productcap"
)

func TestScorecardCheckRepository(t *testing.T) {
	root := filepath.Join("..", "..")
	scorecard, err := Check(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(scorecard.Gates) < 8 {
		t.Fatalf("gate count = %d, want at least 8", len(scorecard.Gates))
	}
}

func TestValidateRejectsUnsortedGateIDs(t *testing.T) {
	err := Validate(Scorecard{Version: 1, Gates: []Gate{
		{ID: "z-last", Name: "z", Category: "activation", Description: "z", Capabilities: []string{"a"}, Targets: []Target{{Metric: "count", Operator: "gte", Value: 1, Unit: "runs"}}, Evidence: []Evidence{{Kind: "runbook", Locator: "docs/STATUS.md"}}},
		{ID: "a-first", Name: "a", Category: "activation", Description: "a", Capabilities: []string{"a"}, Targets: []Target{{Metric: "count", Operator: "gte", Value: 1, Unit: "runs"}}, Evidence: []Evidence{{Kind: "runbook", Locator: "docs/STATUS.md"}}},
	}})
	if err == nil {
		t.Fatal("Validate accepted unsorted gate IDs")
	}
}

func TestValidateRejectsDuplicateCapabilityReferences(t *testing.T) {
	err := Validate(Scorecard{Version: 1, Gates: []Gate{{
		ID: "duplicate", Name: "duplicate", Category: "activation", Description: "duplicate",
		Capabilities: []string{"a", "a"}, Targets: []Target{{Metric: "count", Operator: "gte", Value: 1, Unit: "runs"}}, Evidence: []Evidence{{Kind: "runbook", Locator: "docs/STATUS.md"}},
	}}})
	if err == nil {
		t.Fatal("Validate accepted duplicate capability references")
	}
}

func TestValidateAgainstCatalogRequiresPublicCoverage(t *testing.T) {
	scorecard := Scorecard{Version: 1, Gates: []Gate{{
		ID: "only", Name: "only", Category: "activation", Description: "only",
		Capabilities: []string{"public"}, Targets: []Target{{Metric: "count", Operator: "gte", Value: 1, Unit: "runs"}}, Evidence: []Evidence{{Kind: "runbook", Locator: "docs/STATUS.md"}},
	}}}
	catalog := productcap.Catalog{Version: 1, Capabilities: []productcap.Capability{
		{ID: "public", Maturity: productcap.MaturityBeta},
		{ID: "other", Maturity: productcap.MaturityPreview},
		{ID: "internal", Maturity: productcap.MaturityInternal},
	}}
	if err := ValidateAgainstCatalog(scorecard, catalog); err == nil {
		t.Fatal("ValidateAgainstCatalog accepted uncovered public capability")
	}
}

func TestValidateAgainstCatalogRequiresAcceptanceEvidence(t *testing.T) {
	scorecard := Scorecard{Version: 1, Gates: []Gate{{
		ID: "only", Name: "only", Category: "activation", Description: "only",
		Capabilities: []string{"public"}, Targets: []Target{{Metric: "count", Operator: "gte", Value: 1, Unit: "runs"}}, Evidence: []Evidence{{Kind: "runbook", Locator: "docs/STATUS.md"}},
	}}}
	catalog := productcap.Catalog{Version: 1, Capabilities: []productcap.Capability{{
		ID: "public", Maturity: productcap.MaturityBeta, AcceptanceTest: "pkg/example_test.go::TestExample",
	}}}
	if err := ValidateAgainstCatalog(scorecard, catalog); err == nil {
		t.Fatal("ValidateAgainstCatalog accepted a capability without its acceptance evidence")
	}
}
