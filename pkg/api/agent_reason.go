package api

import (
	"context"
	"net/http"
	"strings"
	"unicode"
)

// AgentReasonHeader carries what an agent says a request is for
// (ADR-20261110182236). apid reads it only from agent and mcp-login keys.
const AgentReasonHeader = "Gregale-Agent-Reason"

// SanitizeAgentReason bounds an agent's reason for display: control and
// format characters become spaces, runs of whitespace collapse to one
// space, the ends are trimmed and the text is cut at MaxAgentReasonChars
// characters. Invalid UTF-8 is dropped. An empty result means no reason.
func SanitizeAgentReason(raw string) string {
	var b strings.Builder
	chars, space := 0, false
	for _, r := range strings.ToValidUTF8(raw, "") {
		if unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			space = b.Len() > 0
			continue
		}
		need := 1
		if space {
			need = 2
		}
		if chars+need > MaxAgentReasonChars {
			break
		}
		if space {
			b.WriteByte(' ')
			chars++
			space = false
		}
		b.WriteRune(r)
		chars++
	}
	return b.String()
}

type agentReasonKey struct{}

// WithAgentReason returns a context whose requests through a Client carry
// reason in the Gregale-Agent-Reason header. An empty reason sends none.
func WithAgentReason(ctx context.Context, reason string) context.Context {
	return context.WithValue(ctx, agentReasonKey{}, SanitizeAgentReason(reason))
}

// AgentReasonFromContext returns the reason WithAgentReason set, if any.
func AgentReasonFromContext(ctx context.Context) string {
	reason, _ := ctx.Value(agentReasonKey{}).(string)
	return reason
}

// agentReasonTransport adds the context's reason to each request that does
// not already carry one.
type agentReasonTransport struct{ next http.RoundTripper }

func (t agentReasonTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	reason := AgentReasonFromContext(req.Context())
	if reason == "" || req.Header.Get(AgentReasonHeader) != "" {
		return t.next.RoundTrip(req)
	}
	out := req.Clone(req.Context())
	out.Header.Set(AgentReasonHeader, reason)
	return t.next.RoundTrip(out)
}
