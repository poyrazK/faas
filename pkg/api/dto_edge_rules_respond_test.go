package api

import (
	"strings"
	"testing"
)

func TestEdgeRuleRespondActionValidate(t *testing.T) {
	valid := &EdgeRuleRespondAction{StatusCode: 200, Body: []byte(`{"days":3,"price":4.99}`)}
	if prob := valid.Validate(); prob != nil {
		t.Fatalf("valid respond action rejected: %v", prob)
	}

	for _, tc := range []struct {
		name string
		a    EdgeRuleRespondAction
	}{
		{name: "informational status", a: EdgeRuleRespondAction{StatusCode: 101}},
		{name: "invalid json", a: EdgeRuleRespondAction{StatusCode: 200, Body: []byte("{")}},
		{name: "body on no content", a: EdgeRuleRespondAction{StatusCode: 204, Body: []byte(`{}`)}},
		{name: "body too large", a: EdgeRuleRespondAction{StatusCode: 500, Body: []byte(strings.Repeat("x", MaxEdgeRuleRespondBodyBytes+1))}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if prob := tc.a.Validate(); prob == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
