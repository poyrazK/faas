package main

// adr: 990 — euro amounts become integer millicents without floating point.

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

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

// adr: 991 — guardrail flags merge into the current set; keys come from stdin.
func TestApplyAIGuardrailFlags(t *testing.T) {
	rpm := int64(60)
	current := api.PutOutboundBindingAIGuardrailsRequest{RequestsPerMinute: &rpm, AllowedModels: []string{"gpt-4o"}}
	got, err := applyAIGuardrailFlags(current, "", "5000", "o3*, gpt-4o-mini", "none")
	if err != nil || got.RequestsPerMinute == nil || *got.RequestsPerMinute != 60 || got.TokensPerMinute == nil || *got.TokensPerMinute != 5000 ||
		len(got.AllowedModels) != 2 || got.AllowedModels[1] != "gpt-4o-mini" || got.MaxOutputTokens != nil || !got.Valid() {
		t.Fatalf("merged guardrails = %+v, %v", got, err)
	}
	cleared, err := applyAIGuardrailFlags(got, "none", "", "none", "")
	if err != nil || cleared.RequestsPerMinute != nil || cleared.AllowedModels != nil || cleared.TokensPerMinute == nil {
		t.Fatalf("cleared guardrails = %+v, %v", cleared, err)
	}
	if _, err := applyAIGuardrailFlags(current, "fast", "", "", ""); err == nil {
		t.Fatal("a non-numeric --rpm was accepted")
	}
}

func TestReadCredentialLine(t *testing.T) {
	if got, err := readCredentialLine(strings.NewReader("aws:AKID:secret\r\nignored\n")); err != nil || got != "aws:AKID:secret" {
		t.Fatalf("line = %q, %v", got, err)
	}
	if _, err := readCredentialLine(strings.NewReader("")); err == nil {
		t.Fatal("empty stdin accepted")
	}
	if aiRouteCallPath(api.OutboundAIRouteFormatOpenAI) != "v1/chat/completions" || aiRouteCallPath("native") != "<provider path>" {
		t.Fatal("route call path")
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
