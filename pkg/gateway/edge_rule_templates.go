package gateway

// ADR-967: request values in header and redirect actions. Templates are
// compiled at host load; here they are expanded per request from the same
// snapshot match conditions read (trusted client IP, lazy country/ASN).

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
)

// edgeRuleTemplateInput snapshots r for template expansion. The path is
// the request's current path (after any rewrite rule).
func edgeRuleTemplateInput(r *http.Request) api.EdgeRuleTemplateInput {
	in := api.EdgeRuleTemplateInput{
		Method: r.Method, Host: hostname(r.Host), Path: r.URL.Path, EscapedPath: r.URL.EscapedPath(),
		RawQuery: r.URL.RawQuery, Headers: r.Header, RequestID: r.Header.Get(api.RequestIDHeader),
	}
	if m := edgeRuleMatchContextFrom(r.Context()); m != nil {
		in.ClientIP = m.ClientIP
		in.Country = m.resolvedCountry
		in.ASN = m.resolvedASN
	}
	return in
}

// expandHeaderOps renders templated ops. Untemplated rules are returned
// as-is, so the common path allocates nothing.
func expandHeaderOps(ops []EdgeRuleHeaderOp, r *http.Request) []EdgeRuleHeaderOp {
	templated := false
	for i := range ops {
		if ops[i].Template != nil {
			templated = true
			break
		}
	}
	if !templated {
		return ops
	}
	in := edgeRuleTemplateInput(r)
	out := make([]EdgeRuleHeaderOp, len(ops))
	for i, op := range ops {
		out[i] = op
		if op.Template != nil {
			out[i].Value = op.Template.Expand(in)
			out[i].Template = nil
		}
	}
	return out
}

// expandRedirect renders a templated redirect's target and headers.
func expandRedirect(rule *EdgeRuleRedirectResolved, r *http.Request) (string, map[string]string) {
	if rule.ToTemplate == nil {
		return rule.To, rule.Headers
	}
	in := edgeRuleTemplateInput(r)
	headers := make(map[string]string, len(rule.HeaderTemplates))
	for name, t := range rule.HeaderTemplates {
		headers[name] = t.Expand(in)
	}
	return rule.ToTemplate.Expand(in), headers
}
