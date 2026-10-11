package gateway

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

type stubASNReader struct {
	asn   uint32
	err   error
	calls int
}

func (s *stubASNReader) LookupASN(net.IP) (uint32, string, bool, error) {
	s.calls++
	if s.err != nil {
		return 0, "", false, s.err
	}
	return s.asn, "Example", s.asn != 0, nil
}

// adr: 966 — an asn condition matches the trusted client IP's autonomous
// system, resolved at most once per request; a reader error or untrusted
// IP leaves the field absent so the condition does not match.
func TestApplicableEdgeRulesASNCondition(t *testing.T) {
	rules := []EdgeRuleResolved{{ID: "cloud", AccountID: "acct", TargetAppSlug: "x",
		EdgeRuleCondition: conditionFromJSON(t, `{"field":"asn","op":"in","values":["AS16509","15169"]}`)}}
	account := func(r *EdgeRuleResolved) string { return r.AccountID }
	matched := func(reader *stubASNReader, clientIP net.IP) bool {
		h := (&Handler{}).WithASNReader(reader)
		req := httptest.NewRequest(http.MethodGet, "http://api.example.com/", nil)
		m := NewEdgeRuleMatchContext(req, clientIP, nil, nil)
		m.SetASNLookup(h.edgeRuleASNLookup())
		ctx := WithEdgeRuleMatchContext(WithEdgeRuleOwner(context.Background(), "acct"), m)
		first := len(ApplicableEdgeRules(ctx, rules, account, "/", http.MethodGet)) == 1
		second := len(ApplicableEdgeRules(ctx, rules, account, "/", http.MethodGet)) == 1
		if first != second {
			t.Fatal("repeated lookups disagree")
		}
		return first
	}
	aws := &stubASNReader{asn: 16509}
	if !matched(aws, net.ParseIP("203.0.113.9")) {
		t.Fatal("AS16509 client must match")
	}
	if aws.calls != 1 {
		t.Fatalf("ASN looked up %d times, want once per request", aws.calls)
	}
	if matched(&stubASNReader{asn: 13335}, net.ParseIP("203.0.113.9")) {
		t.Fatal("AS13335 client matched an AS16509/15169 condition")
	}
	if matched(&stubASNReader{err: errors.New("corrupt db")}, net.ParseIP("203.0.113.9")) {
		t.Fatal("lookup error must leave asn absent")
	}
	untrusted := &stubASNReader{asn: 16509}
	if matched(untrusted, nil) || untrusted.calls != 0 {
		t.Fatal("no trusted client IP: asn must be absent and never looked up")
	}
}
