package main

import (
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestValidateEdgeRuleMetadata(t *testing.T) {
	now := time.Now()
	future, past := now.Add(time.Hour), now.Add(-time.Second)
	str := func(s string) *string { return &s }
	for _, tc := range []struct {
		name              string
		label, desc       *string
		expiresAt         *time.Time
		wantProblemSubstr string
	}{
		{name: "all unset"},
		{name: "valid", label: str("Block scrapers"), desc: str("temporary"), expiresAt: &future},
		{name: "name too long", label: str(strings.Repeat("n", api.EdgeRuleNameMaxChars+1)), wantProblemSubstr: "name exceeds"},
		{name: "description too long", desc: str(strings.Repeat("d", api.EdgeRuleDescriptionMaxChars+1)), wantProblemSubstr: "description exceeds"},
		{name: "expiry in the past", expiresAt: &past, wantProblemSubstr: "in the future"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prob := api.ValidateEdgeRuleMetadata(tc.label, tc.desc, tc.expiresAt, now)
			switch {
			case tc.wantProblemSubstr == "" && prob != nil:
				t.Fatalf("unexpected problem: %s", prob.Detail)
			case tc.wantProblemSubstr != "" && (prob == nil || !strings.Contains(prob.Detail, tc.wantProblemSubstr)):
				t.Fatalf("problem = %v, want one containing %q", prob, tc.wantProblemSubstr)
			}
		})
	}
}

// PATCH distinguishes "leave the expiry alone", "set a new expiry" and
// "remove the expiry" (clear_expires_at) on the way to the store.
func TestEdgeRuleUpdateParamsFromMapsExpiryTriState(t *testing.T) {
	if p := edgeRuleUpdateParamsFrom(api.UpdateEdgeRuleRequest{}, state.EdgeRuleKindIP); p.ExpiresAt != nil {
		t.Fatal("absent expiry touched the column")
	}
	if p := edgeRuleUpdateParamsFrom(api.UpdateEdgeRuleRequest{ClearExpiresAt: true}, state.EdgeRuleKindIP); p.ExpiresAt == nil || *p.ExpiresAt != nil {
		t.Fatal("clear_expires_at did not map to an explicit NULL")
	}
	at := time.Date(2030, 1, 2, 3, 4, 5, 0, time.FixedZone("x", 3600))
	p := edgeRuleUpdateParamsFrom(api.UpdateEdgeRuleRequest{ExpiresAt: &at}, state.EdgeRuleKindIP)
	if p.ExpiresAt == nil || *p.ExpiresAt == nil || !(*p.ExpiresAt).Equal(at) || (*p.ExpiresAt).Location() != time.UTC {
		t.Fatalf("expires_at not mapped as a UTC instant: %v", p.ExpiresAt)
	}
}
