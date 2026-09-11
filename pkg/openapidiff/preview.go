package openapidiff

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/state"
)

// RoutePolicyPreviewRoute is the read-only contract view for one
// (path, method) pair. It joins the customer's declared OpenAPI
// operations with the routes observed by gatewayd and the edge rules
// that would apply to the route.
type RoutePolicyPreviewRoute struct {
	Path     string
	Method   string
	Status   string // matched, declared_only, or observed_only
	Declared bool
	Observed bool
	Covered  bool
	Rules    []RoutePolicyPreviewRule
}

// RoutePolicyPreviewRule is the stable, read-only subset of an edge rule
// needed to explain route coverage. Action is the same JSON object stored by
// the edge-rule API and is intentionally not interpreted here.
type RoutePolicyPreviewRule struct {
	ID           string
	MatchHost    string
	MatchPath    string
	MatchMethods []string
	Priority     int
	Enabled      bool
	Kind         string
	ValidateMode string
	Action       json.RawMessage
}

// BuildRoutePolicyPreview computes a deterministic declared-vs-observed route
// diff and attaches matching edge rules. It is deliberately pure and
// read-only so both the apid endpoint and offline tooling can use the same
// semantics.
func BuildRoutePolicyPreview(spec *Spec, observed []RouteRow, rules []state.EdgeRule) []RoutePolicyPreviewRoute {
	type routeKey struct{ path, method string }
	declared := map[routeKey]struct{}{}
	if spec != nil {
		for path, item := range spec.Paths {
			if item == nil {
				continue
			}
			for method := range item.Methods {
				method = strings.ToLower(strings.TrimSpace(method))
				if method == "" {
					continue
				}
				declared[routeKey{path: path, method: method}] = struct{}{}
			}
		}
	}

	observedSet := map[routeKey]struct{}{}
	for _, row := range observed {
		method, path, ok := splitObservedRoute(row.Route)
		if !ok {
			continue
		}
		observedSet[routeKey{path: path, method: method}] = struct{}{}
	}

	all := make(map[routeKey]struct{}, len(declared)+len(observedSet))
	for key := range declared {
		all[key] = struct{}{}
	}
	for key := range observedSet {
		all[key] = struct{}{}
	}
	keys := make([]routeKey, 0, len(all))
	for key := range all {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].path != keys[j].path {
			return keys[i].path < keys[j].path
		}
		return keys[i].method < keys[j].method
	})

	preview := make([]RoutePolicyPreviewRoute, 0, len(keys))
	for _, key := range keys {
		_, isDeclared := declared[key]
		_, isObserved := observedSet[key]
		status := "observed_only"
		if isDeclared && isObserved {
			status = "matched"
		} else if isDeclared {
			status = "declared_only"
		}
		matching := matchingPreviewRules(key.path, key.method, rules)
		covered := false
		for _, rule := range matching {
			if rule.Enabled {
				covered = true
				break
			}
		}
		preview = append(preview, RoutePolicyPreviewRoute{
			Path: key.path, Method: key.method, Status: status,
			Declared: isDeclared, Observed: isObserved,
			Covered: covered, Rules: matching,
		})
	}
	return preview
}

func splitObservedRoute(label string) (method, path string, ok bool) {
	label = strings.TrimSpace(label)
	if label == "" || label == "__route_other__" {
		return "", "", false
	}
	parts := strings.SplitN(label, " ", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	method, path = strings.ToLower(strings.TrimSpace(parts[0])), strings.TrimSpace(parts[1])
	return method, path, method != "" && path != ""
}

func matchingPreviewRules(path, method string, rules []state.EdgeRule) []RoutePolicyPreviewRule {
	matching := make([]RoutePolicyPreviewRule, 0)
	for _, rule := range rules {
		if rule.MatchPath != "/" && rule.MatchPath != path {
			continue
		}
		if len(rule.MatchMethods) > 0 {
			methodMatches := false
			for _, candidate := range rule.MatchMethods {
				if strings.EqualFold(strings.TrimSpace(candidate), method) {
					methodMatches = true
					break
				}
			}
			if !methodMatches {
				continue
			}
		}
		action, _ := json.Marshal(rule.Action)
		matching = append(matching, RoutePolicyPreviewRule{
			ID: rule.ID, MatchHost: rule.MatchHost, MatchPath: rule.MatchPath,
			MatchMethods: append([]string(nil), rule.MatchMethods...),
			Priority:     rule.Priority, Enabled: rule.Enabled, Kind: string(rule.Kind),
			ValidateMode: rule.ValidateMode, Action: action,
		})
	}
	sort.Slice(matching, func(i, j int) bool {
		if matching[i].Priority != matching[j].Priority {
			return matching[i].Priority < matching[j].Priority
		}
		return matching[i].ID < matching[j].ID
	})
	return matching
}
