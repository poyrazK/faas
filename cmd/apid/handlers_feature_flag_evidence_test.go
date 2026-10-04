package main

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestFlagEvidenceQueryFiltersNamedVariant(t *testing.T) {
	req := httptest.NewRequest("GET", "/?variant=treatment&used=true", nil)
	req.SetPathValue("key", "checkout")
	req.SetPathValue("environment", "production")
	p, _, err := flagEvidenceQuery(req, state.FeatureFlagScope{AccountID: "account", EnvironmentID: "environment"}, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	var filters []map[string]any
	if err := json.Unmarshal(p.EvidenceFilter, &filters); err != nil || len(filters) != 1 || filters[0]["flag"] != "checkout" || filters[0]["type"] != "variant" || filters[0]["value"] != "treatment" || filters[0]["used"] != true {
		t.Fatalf("variant filter = %s (%v)", p.EvidenceFilter, err)
	}

	for _, query := range []string{"?variant=bad.value", "?variant=treatment&value=true"} {
		req := httptest.NewRequest("GET", "/"+query, nil)
		req.SetPathValue("key", "checkout")
		if _, _, err := flagEvidenceQuery(req, state.FeatureFlagScope{AccountID: "account", EnvironmentID: "environment"}, 24*time.Hour); err == nil {
			t.Errorf("accepted invalid query %s", query)
		}
	}
}
