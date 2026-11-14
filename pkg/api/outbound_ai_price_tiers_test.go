package api

import "testing"

func TestOutboundAILongContextTierValid(t *testing.T) {
	for _, tc := range []struct {
		tier *OutboundAILongContextTier
		want bool
	}{
		{nil, true},
		{&OutboundAILongContextTier{AboveInputTokens: 200_000, InputPercent: 200, OutputPercent: 150}, true},
		{&OutboundAILongContextTier{AboveInputTokens: 1_000, InputPercent: 100, OutputPercent: 1_000}, true},
		{&OutboundAILongContextTier{AboveInputTokens: 999, InputPercent: 200, OutputPercent: 150}, false},
		{&OutboundAILongContextTier{AboveInputTokens: 2_000_001, InputPercent: 200, OutputPercent: 150}, false},
		{&OutboundAILongContextTier{AboveInputTokens: 200_000, InputPercent: 99, OutputPercent: 150}, false},
		{&OutboundAILongContextTier{AboveInputTokens: 200_000, InputPercent: 200, OutputPercent: 1_001}, false},
	} {
		if got := tc.tier.Valid(); got != tc.want {
			t.Errorf("%+v: Valid = %v", tc.tier, got)
		}
	}
	above, input, output := (&OutboundAILongContextTier{AboveInputTokens: 1, InputPercent: 2, OutputPercent: 3}).Columns()
	if tier := OutboundAILongContextFromColumns(above, input, output); tier == nil || tier.InputPercent != 2 {
		t.Fatalf("round trip = %+v", tier)
	}
	if OutboundAILongContextFromColumns(above, nil, output) != nil {
		t.Fatal("a missing column made a tier")
	}
	price := int64(-1)
	if ValidOutboundAIPriceEntries([]OutboundAIPriceEntry{{ModelPrefix: "m", CacheWrite1hMillicentsPerMTok: &price}}) {
		t.Fatal("a negative one-hour write price passed")
	}
}
