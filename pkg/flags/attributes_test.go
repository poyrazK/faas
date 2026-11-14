package flags

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// decisionVectors is the cross-language decision contract in
// testdata/decisions.json; the Node, Python and Go SDK tests read the same
// file.
type decisionVectors struct {
	Bundle Bundle `json:"bundle"`
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

func loadDecisionVectors(t *testing.T) decisionVectors {
	t.Helper()
	raw, err := os.ReadFile("testdata/decisions.json")
	if err != nil {
		t.Fatal(err)
	}
	var v decisionVectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	if err := Validate(v.Bundle.Config); err != nil {
		t.Fatalf("vector bundle is invalid: %v", err)
	}
	return v
}

func TestDecisionVectors(t *testing.T) {
	v := loadDecisionVectors(t)
	for _, c := range v.Cases {
		ctx := EvaluationContext{Customer: c.Customer, Subject: c.Subject, Attributes: c.Attributes, Kills: c.Kills}
		var d Decision
		if c.Type == "variant" {
			d = EvaluateVariantWithContext(v.Bundle, c.Flag, ctx, c.Fallback.(string))
		} else {
			d = EvaluateWithContext(v.Bundle, c.Flag, ctx, c.Fallback.(bool))
		}
		if d.Value != c.Value || d.Reason != c.Reason || d.RuleID != c.RuleID {
			t.Errorf("%s: got value=%v reason=%s rule=%q, want value=%v reason=%s rule=%q", c.Name, d.Value, d.Reason, d.RuleID, c.Value, c.Reason, c.RuleID)
		}
	}
}

func TestValidateAttributeConditions(t *testing.T) {
	rule := func(conditions string) Config {
		var c Config
		raw := `{"groups":{},"flags":[{"key":"f","enabled":true,"default":false,"seed":"s","rules":[{"id":"r","attributes":` + conditions + `,"value":true}]}]}`
		if err := json.Unmarshal([]byte(raw), &c); err != nil {
			t.Fatal(err)
		}
		return c
	}
	valid := []string{
		`[{"attribute":"gregale.country","op":"in","values":["DE"]}]`,
		`[{"attribute":"gregale.ua_family","op":"not_in","values":["bot","curl"]}]`,
		`[{"attribute":"app.email","op":"ends_with","values":["@example.com"]}]`,
		`[{"attribute":"app.plan_tier","op":"in","values":["pro"]}]`,
	}
	for _, conditions := range valid {
		if err := Validate(rule(conditions)); err != nil {
			t.Errorf("%s rejected: %v", conditions, err)
		}
	}
	invalid := map[string]string{
		"empty list":            `[]`,
		"unknown gregale attr":  `[{"attribute":"gregale.ip","op":"in","values":["x"]}]`,
		"bare name":             `[{"attribute":"plan","op":"in","values":["x"]}]`,
		"uppercase app name":    `[{"attribute":"app.Plan","op":"in","values":["x"]}]`,
		"ends_with on gateway":  `[{"attribute":"gregale.country","op":"ends_with","values":["E"]}]`,
		"unknown op":            `[{"attribute":"app.plan","op":"regex","values":["x"]}]`,
		"no values":             `[{"attribute":"app.plan","op":"in","values":[]}]`,
		"duplicate value":       `[{"attribute":"app.plan","op":"in","values":["x","x"]}]`,
		"lowercase country":     `[{"attribute":"gregale.country","op":"in","values":["de"]}]`,
		"control character":     `[{"attribute":"app.plan","op":"in","values":["a\u0001"]}]`,
		"too many conditions":   `[` + strings.Repeat(`{"attribute":"app.plan","op":"in","values":["x"]},`, 8) + `{"attribute":"app.plan","op":"in","values":["x"]}]`,
		"value longer than 128": `[{"attribute":"app.plan","op":"in","values":["` + strings.Repeat("x", 129) + `"]}]`,
	}
	for name, conditions := range invalid {
		if err := Validate(rule(conditions)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestAttributeOnlyRuleCountsAsTargeting(t *testing.T) {
	var c Config
	raw := `{"groups":{},"flags":[{"key":"f","enabled":true,"default":false,"seed":"s","rules":[{"id":"r","attributes":[{"attribute":"gregale.country","op":"in","values":["DE"]}],"value":true}]}]}`
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatal(err)
	}
	if err := Validate(c); err != nil {
		t.Fatalf("attribute-only rule rejected: %v", err)
	}
}
