package api

import (
	"fmt"
	"net/http"
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
	foldedGlob := strings.ToLower(glob)
	if folded := strings.ToLower(cleaned); folded != cleaned || foldedGlob != glob {
		return MatchEdgeRulePath(foldedGlob, folded)
	}
	return false, nil
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

// ValidateEdgeRuleMatchHost checks a match_host: 1..253 bytes, and the "@"
// prefix is reserved for EdgeRuleAppHosts.
func ValidateEdgeRuleMatchHost(host string) *Problem {
	if host == "" || len(host) > 253 {
		return ErrValidation("match_host must be 1..253 chars")
	}
	if strings.HasPrefix(host, "@") && !IsEdgeRuleAppHosts(host) {
		return ErrValidation(`match_host may not start with "@" except "@app" (every host the app serves)`)
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
