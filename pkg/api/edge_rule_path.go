package api

import (
	"fmt"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"
)

// MatchEdgeRulePath reports whether an edge rule's match_path glob covers
// requestPath. Globs use path.Match syntax, with the documented extension
// that a trailing "/*" matches anything beneath the prefix: "/api/*"
// covers "/api/v1/users", not just "/api/v1". Plain path.Match stops at
// one segment, so a kind=jwt or kind=ip rule on "/admin/*" left
// "/admin/users/7" unprotected. "" and "*" match every path.
//
// A trailing "/**" matches the prefix itself and anything beneath it:
// "/**" covers every path and "/api/**" covers "/api" and "/api/v1/x".
// Plain path.Match reads "**" as two single-segment stars, so the MCP
// resource policy (which requires match_path=/**) gated "/mcp" but not
// "/mcp/x".
func MatchEdgeRulePath(glob, requestPath string) (bool, error) {
	if glob == "" || glob == "*" {
		return true, nil
	}
	if base, ok := strings.CutSuffix(glob, "/**"); ok {
		if base == "" {
			return strings.HasPrefix(requestPath, "/"), nil
		}
		matched, err := path.Match(base, requestPath)
		if err != nil || matched {
			return matched, err
		}
		return MatchEdgeRulePath(base+"/*", requestPath)
	}
	matched, err := path.Match(glob, requestPath)
	if err != nil || matched {
		return matched, err
	}
	base, ok := strings.CutSuffix(glob, "/*")
	if !ok {
		return false, nil
	}
	// Compare the prefix segment-for-segment so wildcards inside it keep
	// path.Match semantics, then require at least one byte beneath it.
	depth := strings.Count(base, "/")
	cut := -1
	for i, seen := 0, 0; i < len(requestPath); i++ {
		if requestPath[i] == '/' {
			if seen == depth {
				cut = i
				break
			}
			seen++
		}
	}
	if cut < 0 || cut+1 >= len(requestPath) {
		return false, nil
	}
	return path.Match(base, requestPath[:cut])
}

// OpenAPIPathGlob converts an OpenAPI path template into an edge-rule glob.
// Each {param} becomes "?*": one or more characters within a single segment.
// A bare "*" would be wrong for a trailing parameter, because
// MatchEdgeRulePath treats a trailing "/*" as the whole subtree, so
// "/users/{id}" would also match "/users/7/avatar". Literal glob
// metacharacters are escaped. It reports false for a template the glob
// cannot express, such as an unclosed brace.
func OpenAPIPathGlob(template string) (string, bool) {
	return templateGlob(template, true)
}

// EdgeRuleTemplatedPath reports whether matchPath contains OpenAPI-style
// {param} placeholders. Edge-rule paths are globs, so a placeholder only
// matches the literal braces and the rule never runs. It returns the glob the
// author most likely meant; glob syntax already in matchPath is kept.
func EdgeRuleTemplatedPath(matchPath string) (string, bool) {
	if !strings.Contains(matchPath, "{") {
		return "", false
	}
	return templateGlob(matchPath, false)
}

func templateGlob(template string, escape bool) (string, bool) {
	var b strings.Builder
	replaced := false
	for i := 0; i < len(template); i++ {
		switch c := template[i]; c {
		case '{':
			end := strings.IndexByte(template[i:], '}')
			if end <= 1 || strings.ContainsAny(template[i+1:i+end], "/{") {
				return "", false
			}
			b.WriteString("?*")
			replaced = true
			i += end
		case '}':
			return "", false
		case '*', '?', '[', ']', '\\':
			if escape {
				b.WriteByte('\\')
			}
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	glob := b.String()
	if _, err := path.Match(glob, ""); err != nil || !strings.HasPrefix(glob, "/") {
		return "", false
	}
	if !escape && !replaced {
		return "", false
	}
	return glob, true
}

// edgeRuleMethodOverrideHeaders are the headers frameworks read to run a
// request as another method (Express method-override, Laravel, ASP.NET Core,
// Rack); "_method" in the query is the parameter form (Rails, Laravel,
// Spring's HiddenHttpMethodFilter).
var edgeRuleMethodOverrideHeaders = []string{"X-HTTP-Method-Override", "X-HTTP-Method", "X-Method-Override"}

// EdgeRuleMethodOverride returns the method a request asks the app to run it
// as, through a method-override header or a "_method" query parameter, when
// that differs from method; else "". A rule that denies or constrains
// requests and is scoped to methods matches the overridden method too, or a
// POST the app runs as DELETE would pass a DELETE-only gate (ADR-091
// amendment 2026-11-02, later). A "_method" form field in the body is not
// read here.
func EdgeRuleMethodOverride(method string, header http.Header, rawQuery string) string {
	candidates := make([]string, 0, 2)
	for _, name := range edgeRuleMethodOverrideHeaders {
		candidates = append(candidates, header.Values(name)...)
	}
	if strings.Contains(rawQuery, "_method") {
		if q, err := url.ParseQuery(rawQuery); err == nil {
			candidates = append(candidates, q["_method"]...)
		}
	}
	for _, c := range candidates {
		if m := strings.ToUpper(strings.TrimSpace(c)); m != method && slices.Contains(edgeRuleMatchMethods, m) {
			return m
		}
	}
	return ""
}

// EdgeRuleKindUsesProtectivePathMatch reports whether a rule kind denies or
// constrains requests, and therefore matches path variants protectively
// (MatchProtectiveEdgeRulePath) rather than exactly.
func EdgeRuleKindUsesProtectivePathMatch(kind string) bool {
	switch kind {
	case "jwt", "ip", "geo", "limit", "throttle", "validate", "maintenance", "waf", "challenge":
		return true
	}
	return false
}

// MatchProtectiveEdgeRulePath is MatchEdgeRulePath for gates that deny or
// constrain: the rule applies when the raw path, its dot-segment/duplicate-
// slash normalized form, or either compared case-insensitively matches.
// Frameworks that normalize before routing would otherwise serve
// /public/../admin/x or //admin/x as /admin/x, and case-insensitive routers
// serve /ADMIN/x as /admin/x, while the gate compared the raw string. Every
// extra form only adds protection. The gateway and the trace simulator both
// call this so they cannot disagree on which requests a gate covers.
func MatchProtectiveEdgeRulePath(glob, requestPath string) (bool, error) {
	ok, err := MatchEdgeRulePath(glob, requestPath)
	if ok || err != nil {
		return ok, err
	}
	cleaned := path.Clean("/" + strings.ReplaceAll(requestPath, "\\", "/"))
	if cleaned != requestPath {
		if ok, _ := MatchEdgeRulePath(glob, cleaned); ok {
			return true, nil
		}
	}
	// Java servlet containers and Spring route /admin;x/users as
	// /admin/users: they drop a segment's ";" parameters before matching
	// (ADR-091 amendment 2026-11-02, later).
	if bare := stripPathParameters(cleaned); bare != cleaned {
		if ok, _ := MatchEdgeRulePath(glob, bare); ok {
			return true, nil
		}
		cleaned = bare
	}
	foldedGlob := strings.ToLower(glob)
	if folded := strings.ToLower(cleaned); folded != cleaned || foldedGlob != glob {
		return MatchEdgeRulePath(foldedGlob, folded)
	}
	return false, nil
}

// NormalizeEdgeRequestPath resolves "." and ".." segments and collapses
// duplicate slashes in a request path, keeping a trailing slash. The gateway
// forwards this form to the app and every edge rule sees it
// (ADR-20261108100000), so a rule picked for /public/../upload, which most
// frameworks serve as /upload, is a rule for /upload: a higher-priority
// exception on /public/** can no longer be selected by a form the app never
// routes. A path that does not start with "/" (OPTIONS *) is returned as
// given. Backslashes, ";" parameters and case are left alone; gates still
// match those forms protectively (MatchProtectiveEdgeRulePath).
func NormalizeEdgeRequestPath(p string) string {
	if !strings.HasPrefix(p, "/") {
		return p
	}
	if !strings.Contains(p, "//") && !strings.Contains(p, "/.") {
		return p
	}
	cleaned := path.Clean(p)
	if cleaned != "/" && (strings.HasSuffix(p, "/") || strings.HasSuffix(p, "/.") || strings.HasSuffix(p, "/..")) {
		cleaned += "/"
	}
	return cleaned
}

// ProtectiveEdgeRulePaths returns the path forms a rule that denies or
// constrains matches requestPath under (MatchProtectiveEdgeRulePath): the raw
// path, its dot-segment/duplicate-slash normalized form, that form with each
// segment's ";" parameters dropped, and the case-folded forms, each once and
// raw first. Match conditions on such rules hold when they hold for any form
// (ADR-20261107190000), as the match_path selector already does.
func ProtectiveEdgeRulePaths(requestPath string) []string {
	out := []string{requestPath}
	add := func(p string) {
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	cleaned := path.Clean("/" + strings.ReplaceAll(requestPath, "\\", "/"))
	bare := stripPathParameters(cleaned)
	add(cleaned)
	add(bare)
	add(strings.ToLower(cleaned))
	add(strings.ToLower(bare))
	return out
}

// stripPathParameters drops each segment's ";" parameters (RFC 3986 section
// 3.3), as servlet containers do before routing: /a;jsessionid=1/b;v=2
// becomes /a/b.
func stripPathParameters(p string) string {
	if !strings.Contains(p, ";") {
		return p
	}
	segments := strings.Split(p, "/")
	for i, s := range segments {
		segments[i], _, _ = strings.Cut(s, ";")
	}
	return path.Clean("/" + strings.Join(segments, "/"))
}

// edgeRuleMatchMethods is the closed vocabulary of match_methods: the RFC 9110
// methods and PATCH.
var edgeRuleMatchMethods = []string{
	http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch,
	http.MethodDelete, http.MethodOptions, http.MethodConnect, http.MethodTrace,
}

// NormalizeEdgeRuleMatchMethods validates a rule's match_methods and returns
// them upper-cased, without duplicates. The gateway compares methods exactly,
// so an entry outside the vocabulary ("*", a typo, a trailing space) matched
// no request, and a jwt or ip rule scoped by it silently never applied
// (ADR-091 amendment 2026-10-28). An empty list matches every method and is
// returned as given.
func NormalizeEdgeRuleMatchMethods(methods []string) ([]string, *Problem) {
	if len(methods) == 0 {
		return methods, nil
	}
	out := make([]string, 0, len(methods))
	for _, method := range methods {
		if method == "*" {
			return nil, ErrValidation(`match_methods cannot contain "*"; omit match_methods to match every method`)
		}
		upper := strings.ToUpper(method)
		if !slices.Contains(edgeRuleMatchMethods, upper) {
			return nil, ErrValidation(fmt.Sprintf("match_methods entry %q is not an HTTP method (%s)", method, strings.Join(edgeRuleMatchMethods, ", ")))
		}
		if !slices.Contains(out, upper) {
			out = append(out, upper)
		}
	}
	return out, nil
}

// MatchEdgeRuleKindPath applies the matching mode the gateway uses for kind.
func MatchEdgeRuleKindPath(kind, glob, requestPath string) (bool, error) {
	if EdgeRuleKindUsesProtectivePathMatch(kind) {
		return MatchProtectiveEdgeRulePath(glob, requestPath)
	}
	return MatchEdgeRulePath(glob, requestPath)
}

// EdgeRuleAppHosts is the reserved match_host that covers every hostname
// the rule's own app serves: its platform host and its custom domains,
// including ones added later (ADR-1191). It is not a host pattern; the
// gateway attaches such rules to whichever hosts resolve to the rule's app.
const EdgeRuleAppHosts = "@app"

// edgeRuleMatchHostReserved are the characters a match_host may not use.
// The store serves a host's rules with `host LIKE replace(match_host, '*',
// '%')`, where "%" and "_" are wildcards and "[", "]" and "\" are literal,
// while the gateway's cache invalidation, convergence fences and the trace
// simulator match with path.Match, which reads them the other way round. A
// rule using them was served to hosts its edits never refreshed, or shown
// as covering hosts it never applied to (ADR-091 amendment 2026-11-07,
// later). "*" and "?" mean the same in both.
const edgeRuleMatchHostReserved = `%_[]\`

// ValidateEdgeRuleMatchHost checks a match_host: 1..253 bytes, none of the
// characters the two host matchers disagree on, and the "@" prefix is
// reserved for EdgeRuleAppHosts.
func ValidateEdgeRuleMatchHost(host string) *Problem {
	if host == "" || len(host) > 253 {
		return ErrValidation("match_host must be 1..253 chars")
	}
	if strings.HasPrefix(host, "@") && !IsEdgeRuleAppHosts(host) {
		return ErrValidation(`match_host may not start with "@" except "@app" (every host the app serves)`)
	}
	if i := strings.IndexAny(host, edgeRuleMatchHostReserved); i >= 0 {
		return ErrValidation(fmt.Sprintf(`match_host may not contain %q; use "*" for any run of characters and "?" for one`, host[i:i+1]))
	}
	return nil
}

// IsEdgeRuleAppHosts reports whether pattern is the app-wide match_host.
func IsEdgeRuleAppHosts(pattern string) bool {
	return strings.EqualFold(strings.TrimSpace(pattern), EdgeRuleAppHosts)
}

// EdgeRuleHostMatches reports whether a rule's match_host pattern ("*",
// "*.example.com", an exact host, or another path.Match glob) covers host,
// case-insensitively. It mirrors the store's LIKE translation; the gateway
// cache invalidation and the trace simulator share it. The app-wide pattern
// (EdgeRuleAppHosts) reports true for every host: callers that see one have
// already scoped the rule to the request's app (the gateway merges only the
// serving app's app-wide rules; the trace simulator runs per app), and cache
// invalidation must treat it as covering any host.
func EdgeRuleHostMatches(pattern, host string) bool {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	host = strings.ToLower(strings.TrimSpace(host))
	if pattern == "" || host == "" {
		return false
	}
	if pattern == EdgeRuleAppHosts {
		return true
	}
	ok, err := path.Match(pattern, host)
	return err == nil && ok
}
