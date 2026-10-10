package api

// ADR-967: request values in header and redirect actions. A template is
// literal text with ${name} references (and $$ for a literal $), parsed and
// validated once, then expanded per request with values the gateway already
// resolves for match conditions. The same implementation backs apid
// validation, the gateway and the trace simulator.

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Template bounds.
const (
	EdgeRuleTemplateMaxBytes       = 2048
	EdgeRuleTemplateMaxVars        = 16
	EdgeRuleTemplateMaxValueBytes  = 1024
	edgeRuleTemplateMaxSelectorLen = 128
)

// EdgeRuleTemplateMode selects the escaping and the structural rules.
type EdgeRuleTemplateMode int

const (
	// EdgeRuleTemplateHeader expands into an HTTP header value: control
	// characters are dropped and each value is capped.
	EdgeRuleTemplateHeader EdgeRuleTemplateMode = iota
	// EdgeRuleTemplateRedirect expands into a redirect target: values are
	// URL-escaped (path and query keep their already-escaped form), the
	// target must start with a literal "/" or "http(s)://", only ${host}
	// may appear in the authority, and a leading "//" is collapsed.
	EdgeRuleTemplateRedirect
)

// EdgeRuleTemplateInput is the request snapshot a template reads. Country
// and ASN are resolved lazily (GeoIP lookups) and only when referenced.
type EdgeRuleTemplateInput struct {
	Method      string
	Host        string
	Path        string // decoded path
	EscapedPath string // r.URL.EscapedPath()
	RawQuery    string
	Headers     http.Header
	ClientIP    net.IP
	RequestID   string
	Country     func() string
	ASN         func() uint32
}

type templateSegment struct {
	literal string
	kind    string // "" for a literal segment
	name    string
}

// EdgeRuleTemplate is a compiled template, immutable and safe for
// concurrent expansion.
type EdgeRuleTemplate struct {
	mode     EdgeRuleTemplateMode
	segments []templateSegment
}

// CompileEdgeRuleTemplate parses and validates a template.
func CompileEdgeRuleTemplate(s string, mode EdgeRuleTemplateMode) (*EdgeRuleTemplate, error) {
	if len(s) > EdgeRuleTemplateMaxBytes {
		return nil, fmt.Errorf("template longer than %d bytes", EdgeRuleTemplateMaxBytes)
	}
	t := &EdgeRuleTemplate{mode: mode}
	var lit strings.Builder
	vars := 0
	for i := 0; i < len(s); i++ {
		if s[i] != '$' {
			lit.WriteByte(s[i])
			continue
		}
		switch {
		case i+1 < len(s) && s[i+1] == '$':
			lit.WriteByte('$')
			i++
		case i+1 < len(s) && s[i+1] == '{':
			end := strings.IndexByte(s[i+2:], '}')
			if end < 0 {
				return nil, fmt.Errorf("template has an unterminated ${ at byte %d", i)
			}
			ref := s[i+2 : i+2+end]
			kind, name, err := parseTemplateVar(ref)
			if err != nil {
				return nil, err
			}
			if lit.Len() > 0 {
				t.segments = append(t.segments, templateSegment{literal: lit.String()})
				lit.Reset()
			}
			t.segments = append(t.segments, templateSegment{kind: kind, name: name})
			vars++
			i += 2 + end
		default:
			return nil, fmt.Errorf("template has a bare $ at byte %d (use $$ for a literal $ or ${name} for a value)", i)
		}
	}
	if lit.Len() > 0 {
		t.segments = append(t.segments, templateSegment{literal: lit.String()})
	}
	if vars > EdgeRuleTemplateMaxVars {
		return nil, fmt.Errorf("template has more than %d values", EdgeRuleTemplateMaxVars)
	}
	if mode == EdgeRuleTemplateRedirect {
		if err := t.checkRedirectShape(); err != nil {
			return nil, err
		}
	}
	return t, nil
}

func parseTemplateVar(ref string) (string, string, error) {
	switch ref {
	case "host", "path", "method", "query", "client_ip", "country", "asn", "request_id":
		return ref, "", nil
	}
	prefix, name, ok := strings.Cut(ref, ":")
	if ok && name != "" && len(name) <= edgeRuleTemplateMaxSelectorLen {
		switch prefix {
		case "header":
			return prefix, http.CanonicalHeaderKey(name), nil
		case "query", "cookie":
			return prefix, name, nil
		}
	}
	return "", "", fmt.Errorf("unknown template value ${%s} (host, path, method, query, client_ip, country, asn, request_id, header:<name>, query:<name>, cookie:<name>)", ref)
}

// checkRedirectShape keeps request values out of the redirect's scheme and
// authority, so a template cannot become an open redirect.
func (t *EdgeRuleTemplate) checkRedirectShape() error {
	if len(t.segments) == 0 || t.segments[0].kind != "" {
		return fmt.Errorf("redirect template must start with a literal \"/\" or \"http(s)://\"")
	}
	first := t.segments[0].literal
	if strings.HasPrefix(first, "/") {
		if strings.HasPrefix(first, "//") || strings.HasPrefix(first, "/\\") {
			return fmt.Errorf("redirect template must not start with \"//\"")
		}
		return nil
	}
	rest, ok := strings.CutPrefix(first, "https://")
	if !ok {
		if rest, ok = strings.CutPrefix(first, "http://"); !ok {
			return fmt.Errorf("redirect template must start with a literal \"/\" or \"http(s)://\"")
		}
	}
	// Walk the authority: literal text or ${host} until the first "/", "?" or "#".
	if strings.ContainsAny(rest, "/?#") {
		return nil
	}
	for _, seg := range t.segments[1:] {
		if seg.kind == "" {
			if strings.ContainsAny(seg.literal, "/?#") {
				return nil
			}
			continue
		}
		if seg.kind == "path" && seg.name == "" {
			return nil // expands with a leading "/", which ends the authority
		}
		if seg.kind != "host" {
			return fmt.Errorf("redirect template may use only ${host} before the path; ${%s} would let the request choose the redirect's host", seg.ref())
		}
	}
	return nil
}

func (s templateSegment) ref() string {
	if s.name == "" {
		return s.kind
	}
	return s.kind + ":" + s.name
}

// NeedsGeo reports whether expansion reads country or ASN.
func (t *EdgeRuleTemplate) NeedsGeo() bool {
	for _, s := range t.segments {
		if s.kind == "country" || s.kind == "asn" {
			return true
		}
	}
	return false
}

// Expand renders the template for one request. A value the request does
// not carry expands to the empty string.
func (t *EdgeRuleTemplate) Expand(in EdgeRuleTemplateInput) string {
	if t == nil {
		return ""
	}
	var b strings.Builder
	for _, s := range t.segments {
		if s.kind == "" {
			b.WriteString(s.literal)
			continue
		}
		b.WriteString(t.escape(s, t.value(s, in)))
	}
	out := b.String()
	if t.mode == EdgeRuleTemplateRedirect {
		for strings.HasPrefix(out, "//") || strings.HasPrefix(out, "/\\") {
			out = out[1:]
		}
	}
	return out
}

func (t *EdgeRuleTemplate) escape(s templateSegment, v string) string {
	if t.mode == EdgeRuleTemplateHeader {
		v = strings.Map(func(r rune) rune {
			if r < 0x20 || r == 0x7f {
				return -1
			}
			return r
		}, v)
		return truncateUTF8(v, EdgeRuleTemplateMaxValueBytes)
	}
	if s.name == "" && (s.kind == "path" || s.kind == "query") {
		return v // already in escaped form
	}
	if s.kind == "host" {
		return url.PathEscape(v)
	}
	return url.QueryEscape(v)
}

func (t *EdgeRuleTemplate) value(s templateSegment, in EdgeRuleTemplateInput) string {
	if s.kind == "query" && s.name != "" {
		q, _ := url.ParseQuery(in.RawQuery)
		return q.Get(s.name)
	}
	switch s.kind {
	case "host":
		return in.Host
	case "method":
		return in.Method
	case "path":
		if t.mode == EdgeRuleTemplateRedirect {
			if !strings.HasPrefix(in.EscapedPath, "/") {
				return "/" + in.EscapedPath
			}
			return in.EscapedPath
		}
		return in.Path
	case "query":
		return in.RawQuery
	case "request_id":
		return in.RequestID
	case "client_ip":
		if in.ClientIP == nil {
			return ""
		}
		return in.ClientIP.String()
	case "country":
		if in.Country == nil {
			return ""
		}
		return in.Country()
	case "asn":
		if in.ASN == nil {
			return ""
		}
		if n := in.ASN(); n != 0 {
			return strconv.FormatUint(uint64(n), 10)
		}
		return ""
	case "header":
		return in.Headers.Get(s.name)
	case "cookie":
		r := http.Request{Header: in.Headers}
		if c, err := r.Cookie(s.name); err == nil {
			return c.Value
		}
		return ""
	}
	return ""
}

func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n]
}
