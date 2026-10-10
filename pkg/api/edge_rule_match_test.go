package api

import (
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func mustCompileMatch(t *testing.T, raw string) *EdgeRuleMatchProgram {
	t.Helper()
	var expr EdgeRuleMatchExpr
	if err := json.Unmarshal([]byte(raw), &expr); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	p, err := CompileEdgeRuleMatch(&expr)
	if err != nil {
		t.Fatalf("compile %s: %v", raw, err)
	}
	return p
}

func sampleMatchInput() EdgeRuleMatchInput {
	h := http.Header{}
	h.Set("X-Tier", "gold")
	h.Add("X-Region", "eu-west")
	h.Add("X-Region", "eu-central")
	h.Set("Cookie", "beta=1; session=abc")
	return EdgeRuleMatchInput{
		Method: "GET", Path: "/api/v2/orders", Host: "api.example.com",
		Headers: h, Query: url.Values{"debug": {"true"}},
		ClientIP: net.ParseIP("203.0.113.9"), Country: "DE",
	}
}

func TestEdgeRuleMatchEvaluation(t *testing.T) {
	in := sampleMatchInput()
	for _, tc := range []struct {
		expr string
		want bool
	}{
		{`{"field":"cookie:beta","op":"eq","value":"1"}`, true},
		{`{"field":"cookie:beta","op":"eq","value":"0"}`, false},
		{`{"field":"cookie:missing","op":"missing"}`, true},
		{`{"field":"header:x-tier","op":"in","values":["gold","platinum"]}`, true},
		{`{"field":"header:X-Region","op":"prefix","value":"eu-"}`, true},
		{`{"field":"header:X-Region","op":"ne","value":"eu-west"}`, false},
		{`{"field":"header:X-Region","op":"not_in","values":["us-east"]}`, true},
		{`{"field":"query:debug","op":"exists"}`, true},
		{`{"field":"method","op":"eq","value":"get"}`, true},
		{`{"field":"host","op":"suffix","value":".EXAMPLE.com"}`, true},
		{`{"field":"country","op":"in","values":["de","fr"]}`, true},
		{`{"field":"path","op":"regex","value":"^/api/v[0-9]+/"}`, true},
		{`{"field":"client_ip","op":"cidr","values":["203.0.113.0/24"]}`, true},
		{`{"field":"client_ip","op":"eq","value":"203.0.113.9"}`, true},
		{`{"all":[{"field":"cookie:beta","op":"eq","value":"1"},{"not":{"field":"client_ip","op":"cidr","values":["10.0.0.0/8"]}}]}`, true},
		{`{"any":[{"field":"country","op":"eq","value":"US"},{"field":"header:x-tier","op":"eq","value":"bronze"}]}`, false},
	} {
		if got := mustCompileMatch(t, tc.expr).Matches(in); got != tc.want {
			t.Errorf("%s = %v, want %v", tc.expr, got, tc.want)
		}
	}
	var nilProgram *EdgeRuleMatchProgram
	if !nilProgram.Matches(in) {
		t.Error("nil program must match every request")
	}
}

// ADR-962 §5: a client IP or country the gateway does not trust is absent,
// so it can satisfy only "missing" — never a cidr, eq or negated check.
func TestEdgeRuleMatchUntrustedValuesAreAbsent(t *testing.T) {
	in := sampleMatchInput()
	in.ClientIP, in.Country = nil, ""
	for _, tc := range []struct {
		expr string
		want bool
	}{
		{`{"field":"client_ip","op":"cidr","values":["0.0.0.0/0"]}`, false},
		{`{"field":"client_ip","op":"ne","value":"10.0.0.1"}`, false},
		{`{"field":"client_ip","op":"missing"}`, true},
		{`{"field":"country","op":"not_in","values":["RU"]}`, false},
		{`{"not":{"field":"client_ip","op":"cidr","values":["10.0.0.0/8"]}}`, true},
	} {
		if got := mustCompileMatch(t, tc.expr).Matches(in); got != tc.want {
			t.Errorf("%s = %v, want %v", tc.expr, got, tc.want)
		}
	}
}

func TestEdgeRuleMatchValidation(t *testing.T) {
	deep := `{"field":"method","op":"eq","value":"GET"}`
	for range EdgeRuleMatchMaxDepth {
		deep = `{"not":` + deep + `}`
	}
	many := make([]string, EdgeRuleMatchMaxNodes)
	for i := range many {
		many[i] = `{"field":"method","op":"eq","value":"GET"}`
	}
	for _, tc := range []struct{ name, expr, wantErr string }{
		{"two shapes", `{"all":[{"field":"method","op":"eq","value":"GET"}],"field":"path","op":"exists"}`, "exactly one of"},
		{"empty node", `{}`, "exactly one of"},
		{"unknown field", `{"field":"body","op":"exists"}`, "unknown field"},
		{"unknown op", `{"field":"path","op":"like","value":"x"}`, "unknown op"},
		{"eq needs one", `{"field":"path","op":"eq","values":["a","b"]}`, "takes one value"},
		{"exists no value", `{"field":"path","op":"exists","value":"x"}`, "takes no value"},
		{"bad regex", `{"field":"path","op":"regex","value":"("}`, "invalid regex"},
		{"cidr on header", `{"field":"header:x","op":"cidr","value":"10.0.0.0/8"}`, "only to client_ip"},
		{"bad cidr", `{"field":"client_ip","op":"cidr","value":"10.0.0.0/33"}`, "invalid CIDR"},
		{"bad ip", `{"field":"client_ip","op":"eq","value":"nope"}`, "invalid IP"},
		{"prefix on ip", `{"field":"client_ip","op":"prefix","value":"10."}`, "client_ip supports"},
		{"too deep", deep, "deeper than"},
		{"too many nodes", `{"any":[` + strings.Join(many, ",") + `]}`, "more than"},
		{"long value", `{"field":"path","op":"eq","value":"` + strings.Repeat("a", EdgeRuleMatchMaxValueBytes+1) + `"}`, "longer than"},
	} {
		var expr EdgeRuleMatchExpr
		if err := json.Unmarshal([]byte(tc.expr), &expr); err != nil {
			t.Fatalf("%s: decode: %v", tc.name, err)
		}
		prob := ValidateEdgeRuleMatch(&expr)
		if prob == nil || !strings.Contains(prob.Detail, tc.wantErr) {
			t.Errorf("%s: problem = %v, want one containing %q", tc.name, prob, tc.wantErr)
		}
	}
	if prob := ValidateEdgeRuleMatch(nil); prob != nil {
		t.Errorf("nil condition rejected: %v", prob)
	}
}
