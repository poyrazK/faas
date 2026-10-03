package routerequirements

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/edgeruletrace"
)

type pathFamily struct {
	path, sample string
	segments     []string
	parameters   []bool
	variable     bool
}

// Supported families contain canonical decoded paths and nonempty whole
// segment parameters. Other framework syntax is deliberately not sampled.
func parseFamily(path string) (pathFamily, string) {
	if len(path) > api.RouteCoverageMaxPathBytes || !utf8.ValidString(path) || !strings.HasPrefix(path, "/") || strings.Contains(path, "//") || strings.ContainsAny(path, "%?# *[]\\") || strings.IndexFunc(path, unicode.IsControl) >= 0 {
		return pathFamily{}, "unsupported_path_family"
	}
	family := pathFamily{path: path, segments: strings.Split(path[1:], "/")}
	if len(family.segments) > api.RouteCoverageMaxSegments {
		return pathFamily{}, "unsupported_path_family"
	}
	sample, names := append([]string(nil), family.segments...), map[string]bool{}
	for i, segment := range family.segments {
		parameter := strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}")
		if parameter {
			name := segment[1 : len(segment)-1]
			if name == "" || names[name] || strings.ContainsAny(name, "{}:") || strings.IndexFunc(name, unicode.IsSpace) >= 0 {
				return pathFamily{}, "unsupported_path_family"
			}
			names[name], family.variable, sample[i] = true, true, "x"
		} else if strings.ContainsAny(segment, "{}:") || segment == "." || segment == ".." {
			return pathFamily{}, "unsupported_path_family"
		}
		family.parameters = append(family.parameters, parameter)
	}
	family.sample = "/" + strings.Join(sample, "/")
	return family, ""
}

type pathRelation int

const (
	pathNone pathRelation = iota
	pathAll
	pathPartial
	pathUnknown
)

// selectorRelation proves full or disjoint coverage for a small subset of the
// actual gateway language: literals, whole-segment *, and recursive trailing
// /*. A partial selector can win for some values and must not be sampled away.
func selectorRelation(pattern string, family pathFamily) pathRelation {
	if pattern == "" || pattern == "*" {
		return pathAll
	}
	if _, err := api.MatchEdgeRulePath(pattern, "/"); err != nil {
		return pathUnknown
	}
	if !family.variable {
		matched, _ := api.MatchEdgeRulePath(pattern, family.path)
		if matched {
			return pathAll
		}
		return pathNone
	}
	// Even an unsupported glob is safely disjoint when its initial literal
	// segments differ. Do not let unrelated complex rules obscure coverage.
	if strings.HasPrefix(pattern, "/") {
		for i, segment := range strings.Split(pattern[1:], "/") {
			if strings.ContainsAny(segment, "*?[]\\") {
				break
			}
			if i < len(family.segments) && !family.parameters[i] && segment != family.segments[i] {
				return pathNone
			}
		}
	}
	if !strings.HasPrefix(pattern, "/") || strings.ContainsAny(pattern, "?[]\\") {
		return pathUnknown
	}
	segments := strings.Split(pattern[1:], "/")
	for _, segment := range segments {
		if segment != "*" && strings.Contains(segment, "*") {
			return pathUnknown
		}
	}
	recursive := strings.HasSuffix(pattern, "/*")
	if len(family.segments) < len(segments) || !recursive && len(family.segments) != len(segments) {
		return pathNone
	}
	relation := pathAll
	for i, segment := range segments {
		if segment == "*" {
			continue
		}
		if family.parameters[i] {
			// A parameter cannot equal an empty segment.
			if segment == "" {
				return pathNone
			}
			relation = pathPartial
		} else if segment != family.segments[i] {
			return pathNone
		}
	}
	return relation
}

func prefixRelation(prefix string, family pathFamily) pathRelation {
	if prefix == "/" {
		return pathAll
	}
	segments := strings.Split(strings.TrimSuffix(prefix[1:], "/"), "/")
	if len(family.segments) <= len(segments) {
		return pathNone
	}
	relation := pathAll
	for i, segment := range segments {
		if family.parameters[i] {
			relation = pathPartial
		} else if segment != family.segments[i] {
			return pathNone
		}
	}
	return relation
}

// familyWinner returns a policy only when its selection is invariant for the
// whole family. Lower-precedence partial candidates cannot override it.
func familyWinner(context Context, method, kind string, family pathFamily, work *coverageWork) (*api.EdgeRuleResponse, string) {
	type candidate struct {
		rule     api.EdgeRuleResponse
		relation pathRelation
	}
	var candidates []candidate
	var winner *api.EdgeRuleResponse
	for _, rule := range context.Rules {
		if !work.use(1) {
			return nil, "coverage_limit_exceeded"
		}
		if !rule.Enabled || rule.Kind != kind {
			continue
		}
		if !work.use(len(rule.MatchMethods) + len(rule.MatchHost)) {
			return nil, "coverage_limit_exceeded"
		}
		if !edgeruletrace.HostMatches(rule.MatchHost, context.Host) || !edgeruletrace.MethodMatches(rule.MatchMethods, method) {
			continue
		}
		if !work.use(len(family.segments) + len(rule.MatchPath)) {
			return nil, "coverage_limit_exceeded"
		}
		// Invalid selectors are unknown regardless of priority, as in v1.
		if _, err := api.MatchEdgeRulePath(rule.MatchPath, "/"); err != nil {
			return nil, "invalid_path_selector"
		}
		relation := selectorRelation(rule.MatchPath, family)
		if relation == pathNone {
			continue
		}
		candidates = append(candidates, candidate{rule, relation})
		if relation == pathAll && (winner == nil || rule.Priority < winner.Priority) {
			copyRule := rule
			winner = &copyRule
		}
	}
	for _, candidate := range candidates {
		if winner != nil && candidate.rule.Priority > winner.Priority {
			continue
		}
		if candidate.relation != pathAll {
			return nil, "policy_varies_with_path"
		}
		if winner != nil && candidate.rule.ID != winner.ID && candidate.rule.Priority == winner.Priority {
			return nil, "equal_priority_candidates"
		}
	}
	if winner != nil && len(winner.MatchHeaders) > 0 {
		return nil, "header_dependent_rule"
	}
	return winner, ""
}
