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
func MatchEdgeRulePath(glob, requestPath string) (bool, error) {
	if glob == "" || glob == "*" {
		return true, nil
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
