package preview

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
)

type flagDecisionVectors struct {
	Bundle json.RawMessage `json:"bundle"`
	Cases  []struct {
		Name       string            `json:"name"`
		Flag       string            `json:"flag"`
		Type       string            `json:"type"`
		Customer   string            `json:"customer"`
		Subject    string            `json:"subject"`
		Attributes map[string]string `json:"attributes"`
		Kills      []string          `json:"kills"`
		Fallback   any               `json:"fallback"`
		Value      any               `json:"value"`
		Reason     string            `json:"reason"`
		RuleID     string            `json:"rule_id"`
	} `json:"cases"`
}

func loadFlagDecisionVectors(t *testing.T) (flagDecisionVectors, *runtimeFlagsBundle) {
	t.Helper()
	raw, err := os.ReadFile("../../../pkg/flags/testdata/decisions.json")
	if err != nil {
		t.Fatal(err)
	}
	var v flagDecisionVectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	bundle, err := validateRuntimeBundle(v.Bundle)
	if err != nil {
		t.Fatalf("vector bundle rejected: %v", err)
	}
	return v, bundle
}

// ADR 20261114150000: the Go SDK agrees with pkg/flags, Node and Python on
// the shared decision vectors.
func TestFlagDecisionVectors(t *testing.T) {
	v, bundle := loadFlagDecisionVectors(t)
	for _, c := range v.Cases {
		var d FlagDecision
		if c.Type == "variant" {
			d = evaluateRuntimeVariantWithAttributes(bundle, c.Flag, c.Customer, c.Subject, c.Attributes, c.Kills, c.Fallback.(string))
		} else {
			d = evaluateRuntimeBooleanWithAttributes(bundle, c.Flag, c.Customer, c.Subject, c.Attributes, c.Kills, c.Fallback.(bool))
		}
		if d.Value != c.Value || d.Reason != c.Reason || d.RuleID != c.RuleID {
			t.Errorf("%s: got value=%v reason=%s rule=%q, want value=%v reason=%s rule=%q", c.Name, d.Value, d.Reason, d.RuleID, c.Value, c.Reason, c.RuleID)
		}
	}
}

func TestFlagRequestAttributesFromGatewayAndApplication(t *testing.T) {
	_, bundle := loadFlagDecisionVectors(t)
	f := &GregaleFlags{}
	tenant := "00000000-0000-4000-8000-000000000001"
	request := func(country, family string) context.Context {
		h := http.Header{}
		h.Set(GregaleFlagCustomerHeader, tenant)
		if country != "" {
			h.Set(GregaleClientCountryHeader, country)
		}
		if family != "" {
			h.Set(GregaleClientUAFamilyHeader, family)
		}
		state := &flagRequestState{owner: f, customer: tenant, bundle: bundle, fresh: true, evidence: map[string]FlagEvidence{}, inherit: map[string]flagPropagatedDecision{}}
		state.attributes = map[string]string{}
		if flagCountryPattern.MatchString(country) {
			state.attributes[flagAttributeCountry] = country
		}
		if validFlagKey(family) {
			state.attributes[flagAttributeUAFamily] = family
		}
		return context.WithValue(context.Background(), flagRequestContextKey{}, state)
	}
	if d, err := f.Boolean(request("DE", ""), "eu-checkout", false); err != nil || d.Value != true || d.RuleID != "eu" {
		t.Fatalf("DE: %+v %v", d, err)
	}
	killedCtx := request("DE", "")
	killedCtx.Value(flagRequestContextKey{}).(*flagRequestState).kills = map[string]struct{}{"eu-checkout": {}}
	if d, _ := f.Boolean(killedCtx, "eu-checkout", false); d.Value != false || d.Reason != "killed" {
		t.Fatalf("gateway kill ignored: %+v", d)
	}
	if d, _ := f.Boolean(request("", ""), "risky-path", false); d.Value != false || d.Reason != "killed" {
		t.Fatalf("bundle kill ignored: %+v", d)
	}
	if d, _ := f.Boolean(request("de", ""), "eu-checkout", false); d.Value != false {
		t.Fatalf("malformed country matched: %+v", d)
	}
	ctx, err := f.WithAttributes(request("", "chrome"), map[string]string{"plan": "pro"})
	if err != nil {
		t.Fatal(err)
	}
	if d, _ := f.Boolean(ctx, "pro-only", false); d.Value != true {
		t.Fatalf("app attribute ignored: %+v", d)
	}
	if _, err := f.WithAttributes(request("", ""), map[string]string{"Bad-Name": "x"}); err == nil {
		t.Fatal("invalid attribute name accepted")
	}
	ctx, _ = f.WithAttributes(request("", "chrome"), map[string]string{"plan": "pro\n"})
	if d, _ := f.Boolean(ctx, "pro-only", false); d.Value != false {
		t.Fatalf("invalid value matched: %+v", d)
	}
}
