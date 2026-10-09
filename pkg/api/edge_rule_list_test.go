package api

import (
	"net"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeEdgeRuleListItems(t *testing.T) {
	cases := []struct {
		kind    string
		items   []string
		want    []string
		wantErr string
	}{
		{kind: "ip", items: []string{"10.1.2.3/8", "2001:DB8::1", " 192.0.2.1 ", "192.0.2.1"}, want: []string{"10.0.0.0/8", "192.0.2.1", "2001:db8::1"}},
		{kind: "ip", items: []string{"10.0.0.0/33"}, wantErr: "invalid CIDR"},
		{kind: "ip", items: []string{"example.com"}, wantErr: "invalid IP"},
		{kind: "country", items: []string{"de", "FR", "De"}, want: []string{"DE", "FR"}},
		{kind: "country", items: []string{"XX"}, wantErr: "reserved"},
		{kind: "host", items: []string{"API.Example.com", "*.example.com"}, want: []string{"*.example.com", "api.example.com"}},
		{kind: "host", items: []string{"a.*.example.com"}, wantErr: "invalid host"},
		{kind: "host", items: []string{"-bad.example.com"}, wantErr: "invalid host"},
		{kind: "string", items: []string{"Bot/1.0", "bot/1.0"}, want: []string{"Bot/1.0", "bot/1.0"}},
		{kind: "string", items: []string{""}, wantErr: "empty item"},
		{kind: "string", items: []string{strings.Repeat("x", EdgeRuleMatchMaxValueBytes+1)}, wantErr: "longer than"},
		{kind: "asn", items: []string{"1"}, wantErr: "unknown list kind"},
	}
	for _, tc := range cases {
		got, err := NormalizeEdgeRuleListItems(tc.kind, tc.items)
		if tc.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("%s %v: err=%v, want %q", tc.kind, tc.items, err, tc.wantErr)
			}
			continue
		}
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s %v = %v, %v; want %v", tc.kind, tc.items, got, err, tc.want)
		}
	}
}

func TestValidateEdgeRuleListName(t *testing.T) {
	for _, ok := range []string{"office-ips", "a", "eu_countries", strings.Repeat("a", 64)} {
		if err := ValidateEdgeRuleListName(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "-x", "Office", "a b", strings.Repeat("a", 65)} {
		if ValidateEdgeRuleListName(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func mustEdgeRuleList(t *testing.T, kind string, items ...string) *EdgeRuleList {
	t.Helper()
	l, err := CompileEdgeRuleList(kind, items)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestEdgeRuleMatchInList(t *testing.T) {
	lists := EdgeRuleLists{
		"office":   mustEdgeRuleList(t, "ip", "203.0.113.0/24", "2001:db8::1"),
		"eu":       mustEdgeRuleList(t, "country", "DE", "FR"),
		"partners": mustEdgeRuleList(t, "host", "partner.example", "*.trusted.example"),
		"bots":     mustEdgeRuleList(t, "string", "BadBot/1.0"),
	}
	in := EdgeRuleMatchInput{
		Method:   "GET",
		Path:     "/x",
		Host:     "API.Trusted.Example",
		Headers:  http.Header{"User-Agent": {"BadBot/1.0"}},
		ClientIP: net.ParseIP("203.0.113.9"),
		Country:  "de",
	}
	cases := []struct {
		expr EdgeRuleMatchExpr
		in   EdgeRuleMatchInput
		want bool
	}{
		{EdgeRuleMatchExpr{Field: "client_ip", Op: "in_list", List: "office"}, in, true},
		{EdgeRuleMatchExpr{Field: "client_ip", Op: "in_list", List: "office"}, func() EdgeRuleMatchInput { c := in; c.ClientIP = net.ParseIP("2001:DB8:0::1"); return c }(), true},
		{EdgeRuleMatchExpr{Field: "client_ip", Op: "in_list", List: "office"}, func() EdgeRuleMatchInput { c := in; c.ClientIP = net.ParseIP("198.51.100.1"); return c }(), false},
		{EdgeRuleMatchExpr{Field: "client_ip", Op: "in_list", List: "office"}, func() EdgeRuleMatchInput { c := in; c.ClientIP = nil; return c }(), false},
		{EdgeRuleMatchExpr{Not: &EdgeRuleMatchExpr{Field: "client_ip", Op: "in_list", List: "office"}}, in, false},
		{EdgeRuleMatchExpr{Field: "country", Op: "in_list", List: "eu"}, in, true},
		{EdgeRuleMatchExpr{Field: "country", Op: "in_list", List: "eu"}, func() EdgeRuleMatchInput { c := in; c.Country = "US"; return c }(), false},
		{EdgeRuleMatchExpr{Field: "host", Op: "in_list", List: "partners"}, in, true},
		{EdgeRuleMatchExpr{Field: "host", Op: "in_list", List: "partners"}, func() EdgeRuleMatchInput { c := in; c.Host = "trusted.example"; return c }(), false},
		{EdgeRuleMatchExpr{Field: "host", Op: "in_list", List: "partners"}, func() EdgeRuleMatchInput { c := in; c.Host = "partner.example"; return c }(), true},
		{EdgeRuleMatchExpr{Field: "header:user-agent", Op: "in_list", List: "bots"}, in, true},
		{EdgeRuleMatchExpr{Field: "header:user-agent", Op: "in_list", List: "bots"}, func() EdgeRuleMatchInput { c := in; c.Headers = http.Header{"User-Agent": {"badbot/1.0"}}; return c }(), false},
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
}

func TestEdgeRuleMatchInListValidation(t *testing.T) {
	lists := EdgeRuleLists{
		"office": mustEdgeRuleList(t, "ip", "203.0.113.0/24"),
		"bots":   mustEdgeRuleList(t, "string", "x"),
	}
	cases := []struct {
		expr    EdgeRuleMatchExpr
		wantErr string
	}{
		{EdgeRuleMatchExpr{Field: "client_ip", Op: "in_list", List: "missing"}, `unknown list "missing"`},
		{EdgeRuleMatchExpr{Field: "country", Op: "in_list", List: "office"}, "cannot match field"},
		{EdgeRuleMatchExpr{Field: "method", Op: "in_list", List: "bots"}, "cannot match field"},
		{EdgeRuleMatchExpr{Field: "client_ip", Op: "in_list"}, "takes a list name"},
		{EdgeRuleMatchExpr{Field: "client_ip", Op: "in_list", List: "office", Value: "1.2.3.4"}, "takes a list name"},
		{EdgeRuleMatchExpr{Field: "client_ip", Op: "eq", Value: "1.2.3.4", List: "office"}, "only to op in_list"},
		{EdgeRuleMatchExpr{All: []EdgeRuleMatchExpr{{Field: "path", Op: "eq", Value: "/"}}, List: "office"}, "exactly one of"},
	}
	for i, tc := range cases {
		_, err := CompileEdgeRuleMatchWithLists(&tc.expr, lists)
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("case %d: err=%v, want %q", i, err, tc.wantErr)
		}
	}
	// Without lists, any in_list reference fails to compile, but passes the
	// shape-only check the CLI runs before sending.
	ref := &EdgeRuleMatchExpr{Field: "client_ip", Op: "in_list", List: "office"}
	if _, err := CompileEdgeRuleMatch(ref); err == nil {
		t.Fatal("in_list compiled without lists")
	}
	if prob := ValidateEdgeRuleMatch(ref); prob != nil {
		t.Fatalf("shape-only validation rejected a list reference: %v", prob.Detail)
	}
	if prob := ValidateEdgeRuleMatch(&EdgeRuleMatchExpr{Field: "client_ip", Op: "in_list", List: "Bad Name"}); prob == nil {
		t.Fatal("shape-only validation accepted an invalid list name")
	}
}

func TestEdgeRuleMatchListRefs(t *testing.T) {
	expr := &EdgeRuleMatchExpr{Any: []EdgeRuleMatchExpr{
		{Field: "client_ip", Op: "in_list", List: "office"},
		{Not: &EdgeRuleMatchExpr{Field: "country", Op: "in_list", List: "eu"}},
		{All: []EdgeRuleMatchExpr{{Field: "client_ip", Op: "in_list", List: "office"}}},
	}}
	if got := EdgeRuleMatchListRefs(expr); !reflect.DeepEqual(got, []string{"eu", "office"}) {
		t.Fatalf("refs = %v", got)
	}
	if got := EdgeRuleMatchListRefs(nil); got != nil {
		t.Fatalf("nil refs = %v", got)
	}
}
