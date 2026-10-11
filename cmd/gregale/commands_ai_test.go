package main

// adr: 983 — euro amounts become integer millicents without floating point.

import "testing"

func TestParseEuroMillicents(t *testing.T) {
	for in, want := range map[string]int64{
		"0": 0, "12": 1_200_000, "12.5": 1_250_000, "0.15": 15_000, ".075": 7_500, "0.00001": 1,
	} {
		if got, err := parseEuroMillicents(in); err != nil || got != want {
			t.Errorf("parseEuroMillicents(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "-1", "+1", "1.000001", "1,5", "abc", "1e3", "99999999"} {
		if _, err := parseEuroMillicents(bad); err == nil {
			t.Errorf("parseEuroMillicents(%q) accepted", bad)
		}
	}
}

func TestParseAIPriceSpec(t *testing.T) {
	entry, err := parseAIPriceSpec("model=gpt-4o-mini,input=0.15,cached=0.075,output=0.60")
	if err != nil || entry.ModelPrefix != "gpt-4o-mini" || entry.InputMillicentsPerMTok != 15_000 ||
		entry.CachedInputMillicentsPerMTok == nil || *entry.CachedInputMillicentsPerMTok != 7_500 ||
		entry.OutputMillicentsPerMTok != 60_000 || entry.CacheWriteMillicentsPerMTok != nil {
		t.Fatalf("price spec = %+v, %v", entry, err)
	}
	for _, bad := range []string{"model=m,input=1", "model=m,input=1,output=2,input=3", "model=m,input=1,output=2,colour=red", "input=1,output=2"} {
		if _, err := parseAIPriceSpec(bad); err == nil {
			t.Errorf("parseAIPriceSpec(%q) accepted", bad)
		}
	}
}

func TestParseAIRouteTargets(t *testing.T) {
	targets, err := parseAIRouteTargets([]string{"11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222:gpt-4o-mini"})
	if err != nil || len(targets) != 2 || targets[1].Model != "gpt-4o-mini" || targets[0].Model != "" {
		t.Fatalf("targets = %+v, %v", targets, err)
	}
	if _, err := parseAIRouteTargets(nil); err == nil {
		t.Fatal("empty target list accepted")
	}
}
