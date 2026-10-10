package api

import (
	"net"
	"net/http"
	"strings"
	"testing"
)

func templateInput() EdgeRuleTemplateInput {
	h := http.Header{}
	h.Set("X-Tenant", "acme")
	h.Set("Cookie", "plan=pro")
	h.Set("X-Evil", "a\r\nSet-Cookie: x=1")
	return EdgeRuleTemplateInput{
		Method: "GET", Host: "api.example.com", Path: "/docs/a b", EscapedPath: "/docs/a%20b",
		RawQuery: "lang=tr&q=x%26y", Headers: h, ClientIP: net.ParseIP("203.0.113.9"), RequestID: "req-1",
		Country: func() string { return "DE" }, ASN: func() uint32 { return 13335 },
	}
}

// adr: 967 — header templates expand request values, drop control
// characters, and leave unknown values empty.
func TestEdgeRuleHeaderTemplate(t *testing.T) {
	cases := map[string]string{
		"${country}/${asn}/${client_ip}":                "DE/13335/203.0.113.9",
		"tenant=${header:x-tenant} plan=${cookie:plan}": "tenant=acme plan=pro",
		"${method} ${host}${path}?${query}":             "GET api.example.com/docs/a b?lang=tr&q=x%26y",
		"lang=${query:lang} q=${query:q}":               "lang=tr q=x&y",
		"cost: $$5 id=${request_id}":                    "cost: $5 id=req-1",
		"${header:x-evil}":                              "aSet-Cookie: x=1",
		"missing=[${header:x-none}]":                    "missing=[]",
	}
	for tmpl, want := range cases {
		c, err := CompileEdgeRuleTemplate(tmpl, EdgeRuleTemplateHeader)
		if err != nil {
			t.Fatalf("%q: %v", tmpl, err)
		}
		if got := c.Expand(templateInput()); got != want {
			t.Errorf("%q = %q, want %q", tmpl, got, want)
		}
	}
	long, _ := CompileEdgeRuleTemplate("${header:x-long}", EdgeRuleTemplateHeader)
	in := templateInput()
	in.Headers.Set("X-Long", strings.Repeat("é", EdgeRuleTemplateMaxValueBytes))
	if got := long.Expand(in); len(got) > EdgeRuleTemplateMaxValueBytes {
		t.Fatalf("value not capped: %d bytes", len(got))
	}
	geo, _ := CompileEdgeRuleTemplate("${asn}", EdgeRuleTemplateHeader)
	plain, _ := CompileEdgeRuleTemplate("${host}", EdgeRuleTemplateHeader)
	if !geo.NeedsGeo() || plain.NeedsGeo() {
		t.Fatal("NeedsGeo misreports")
	}
}

// adr: 967 — redirect templates escape values, keep request values out of
// the scheme and host, and cannot become protocol-relative.
func TestEdgeRuleRedirectTemplate(t *testing.T) {
	cases := map[string]string{
		"https://new.example${path}?${query}":        "https://new.example/docs/a%20b?lang=tr&q=x%26y",
		"https://${host}/v2${path}":                  "https://api.example.com/v2/docs/a%20b",
		"/docs?from=${header:x-tenant}&c=${country}": "/docs?from=acme&c=DE",
		"/x?q=${query:q}":                            "/x?q=x%26y",
	}
	for tmpl, want := range cases {
		c, err := CompileEdgeRuleTemplate(tmpl, EdgeRuleTemplateRedirect)
		if err != nil {
			t.Fatalf("%q: %v", tmpl, err)
		}
		if got := c.Expand(templateInput()); got != want {
			t.Errorf("%q = %q, want %q", tmpl, got, want)
		}
	}
	// A request path of "//evil.example/x" must not produce a
	// protocol-relative redirect.
	c, _ := CompileEdgeRuleTemplate("/${path}", EdgeRuleTemplateRedirect)
	in := templateInput()
	in.EscapedPath = "//evil.example/x"
	if got := c.Expand(in); strings.HasPrefix(got, "//") {
		t.Fatalf("open redirect: %q", got)
	}
}

func TestEdgeRuleTemplateValidation(t *testing.T) {
	bad := []struct {
		tmpl string
		mode EdgeRuleTemplateMode
		want string
	}{
		{"${nope}", EdgeRuleTemplateHeader, "unknown template value"},
		{"a $ b", EdgeRuleTemplateHeader, "bare $"},
		{"${host", EdgeRuleTemplateHeader, "unterminated"},
		{"${header:}", EdgeRuleTemplateHeader, "unknown template value"},
		{strings.Repeat("${host}", EdgeRuleTemplateMaxVars+1), EdgeRuleTemplateHeader, "more than"},
		{strings.Repeat("x", EdgeRuleTemplateMaxBytes+1), EdgeRuleTemplateHeader, "longer than"},
		{"${path}", EdgeRuleTemplateRedirect, "must start with a literal"},
		{"//evil.example${path}", EdgeRuleTemplateRedirect, "must not start with"},
		{"ftp://x${path}", EdgeRuleTemplateRedirect, "must start with a literal"},
		{"https://${header:x-host}/x", EdgeRuleTemplateRedirect, "only ${host}"},
		{"https://evil.${query:d}/x", EdgeRuleTemplateRedirect, "only ${host}"},
	}
	for _, tc := range bad {
		if _, err := CompileEdgeRuleTemplate(tc.tmpl, tc.mode); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q: err=%v, want %q", tc.tmpl, err, tc.want)
		}
	}
	for _, ok := range []string{"https://${host}.cdn.example/x", "https://a.example/${header:x}", "/${path}"} {
		if _, err := CompileEdgeRuleTemplate(ok, EdgeRuleTemplateRedirect); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
}
