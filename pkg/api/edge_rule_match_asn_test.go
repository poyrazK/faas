package api

import (
	"reflect"
	"strings"
	"testing"
)

// adr: 966 — the asn field matches the client's autonomous system, accepts
// "AS" prefixes, is absent when unknown, and works with asn lists.
func TestEdgeRuleMatchASN(t *testing.T) {
	cloud := mustEdgeRuleList(t, EdgeRuleListKindASN, "AS16509", "15169")
	lists := EdgeRuleLists{"clouds": cloud}
	in := EdgeRuleMatchInput{ASN: 16509}
	cases := []struct {
		expr EdgeRuleMatchExpr
		in   EdgeRuleMatchInput
		want bool
	}{
		{EdgeRuleMatchExpr{Field: "asn", Op: "eq", Value: "AS16509"}, in, true},
		{EdgeRuleMatchExpr{Field: "asn", Op: "in", Values: []string{"13335", "as16509"}}, in, true},
		{EdgeRuleMatchExpr{Field: "asn", Op: "ne", Value: "16509"}, in, false},
		{EdgeRuleMatchExpr{Field: "asn", Op: "in_list", List: "clouds"}, in, true},
		{EdgeRuleMatchExpr{Field: "asn", Op: "in_list", List: "clouds"}, EdgeRuleMatchInput{ASN: 13335}, false},
		{EdgeRuleMatchExpr{Field: "asn", Op: "exists"}, EdgeRuleMatchInput{}, false},
		{EdgeRuleMatchExpr{Field: "asn", Op: "missing"}, EdgeRuleMatchInput{}, true},
		{EdgeRuleMatchExpr{Field: "asn", Op: "eq", Value: "16509"}, EdgeRuleMatchInput{}, false},
	}
	for i, tc := range cases {
		p, err := CompileEdgeRuleMatchWithLists(&tc.expr, lists)
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		if got := p.Matches(tc.in); got != tc.want {
			t.Errorf("case %d (%+v): got %v, want %v", i, tc.expr, got, tc.want)
		}
	}
	for _, bad := range []EdgeRuleMatchExpr{
		{Field: "asn", Op: "eq", Value: "cloudflare"},
		{Field: "asn", Op: "eq", Value: "0"},
		{Field: "asn", Op: "prefix", Value: "16"},
		{Field: "country", Op: "in_list", List: "clouds"},
	} {
		if _, err := CompileEdgeRuleMatchWithLists(&bad, lists); err == nil {
			t.Errorf("%+v compiled", bad)
		}
	}
	items, err := NormalizeEdgeRuleListItems(EdgeRuleListKindASN, []string{"AS13335", "13335", " as15169 "})
	if err != nil || !reflect.DeepEqual(items, []string{"13335", "15169"}) {
		t.Fatalf("normalize = %v, %v", items, err)
	}
	if _, err := NormalizeEdgeRuleListItems(EdgeRuleListKindASN, []string{"AS4294967296"}); err == nil || !strings.Contains(err.Error(), "invalid ASN") {
		t.Fatalf("out-of-range ASN err = %v", err)
	}
}
