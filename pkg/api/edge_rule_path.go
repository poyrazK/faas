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
