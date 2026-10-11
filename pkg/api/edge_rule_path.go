package api

import (
	"path"
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

// EdgeRuleKindUsesProtectivePathMatch reports whether a rule kind denies or
// constrains requests, and therefore matches path variants protectively
// (MatchProtectiveEdgeRulePath) rather than exactly.
func EdgeRuleKindUsesProtectivePathMatch(kind string) bool {
	switch kind {
	case "jwt", "ip", "geo", "limit", "throttle", "validate", "maintenance":
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

// MatchEdgeRuleKindPath applies the matching mode the gateway uses for kind.
func MatchEdgeRuleKindPath(kind, glob, requestPath string) (bool, error) {
	if EdgeRuleKindUsesProtectivePathMatch(kind) {
		return MatchProtectiveEdgeRulePath(glob, requestPath)
	}
	return MatchEdgeRulePath(glob, requestPath)
}

// EdgeRuleHostMatches reports whether a rule's match_host pattern ("*",
// "*.example.com", an exact host, or another path.Match glob) covers host,
// case-insensitively. It mirrors the store's LIKE translation; the gateway
// cache invalidation and the trace simulator share it.
func EdgeRuleHostMatches(pattern, host string) bool {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	host = strings.ToLower(strings.TrimSpace(host))
	if pattern == "" || host == "" {
		return false
	}
	ok, err := path.Match(pattern, host)
	return err == nil && ok
}
