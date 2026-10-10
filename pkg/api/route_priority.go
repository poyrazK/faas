package api

import (
	"fmt"
	"strings"
)

// Route priority classes (ADR-947). Routes not listed are normal priority.
const (
	RoutePriorityCritical = "critical"
	RoutePriorityBulk     = "bulk"
)

// Where an app's effective route priorities come from.
const (
	RoutePrioritySourceConfigured  = "configured"
	RoutePrioritySourceRouteHealth = "route_health"
	RoutePrioritySourceNone        = "none"
)

// RoutePriorityRule assigns a class to requests matching a method and path.
// Path is a route template (/users/{id}) or an edge-rule glob (/exports/*).
// An empty method matches every method.
type RoutePriorityRule struct {
	Method string `json:"method,omitempty"`
	Path   string `json:"path"`
	Class  string `json:"class"`
}

// RoutePrioritiesResponse is an app's effective route priorities. Source is
// configured (saved rules), route_health (the route-health selectors, treated
// as critical, when nothing is saved) or none.
type RoutePrioritiesResponse struct {
	Slug      string              `json:"slug"`
	Source    string              `json:"source"`
	Routes    []RoutePriorityRule `json:"routes"`
	UpdatedAt string              `json:"updated_at,omitempty"`
}

// SetRoutePrioritiesRequest replaces an app's saved route priorities. An empty
// list saves "no priorities", which also turns off the route-health default.
type SetRoutePrioritiesRequest struct {
	Routes []RoutePriorityRule `json:"routes"`
}

var routePriorityMethods = map[string]bool{"GET": true, "HEAD": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true, "OPTIONS": true}

// ValidateRoutePriorities checks a rule list as saved by apid.
func ValidateRoutePriorities(rules []RoutePriorityRule) error {
	if len(rules) > RoutePriorityMaxRules {
		return fmt.Errorf("routes has %d entries; at most %d are allowed", len(rules), RoutePriorityMaxRules)
	}
	for i, r := range rules {
		if r.Class != RoutePriorityCritical && r.Class != RoutePriorityBulk {
			return fmt.Errorf("routes[%d].class must be %q or %q", i, RoutePriorityCritical, RoutePriorityBulk)
		}
		if r.Method != "" && !routePriorityMethods[r.Method] {
			return fmt.Errorf("routes[%d].method %q is not an HTTP method (use upper case, or omit for any method)", i, r.Method)
		}
		if len(r.Path) > RouteHealthMaxPathBytes {
			return fmt.Errorf("routes[%d].path exceeds %d bytes", i, RouteHealthMaxPathBytes)
		}
		if _, ok := RoutePriorityGlob(r.Path); !ok {
			return fmt.Errorf("routes[%d].path %q must start with / and use {param} or * segments", i, r.Path)
		}
	}
	return nil
}

// RoutePriorityGlob converts a route template to an edge-rule path glob: a
// "{param}" segment becomes "*". Plain globs pass through.
func RoutePriorityGlob(path string) (string, bool) {
	if !strings.HasPrefix(path, "/") || strings.ContainsAny(path, "?#\x00\r\n\t ") {
		return "", false
	}
	parts := strings.Split(path, "/")
	for i, p := range parts {
		if len(p) > 2 && strings.HasPrefix(p, "{") && strings.HasSuffix(p, "}") {
			parts[i] = "*"
		} else if strings.ContainsAny(p, "{}") {
			return "", false
		}
	}
	glob := strings.Join(parts, "/")
	if _, err := MatchEdgeRulePath(glob, "/"); err != nil {
		return "", false
	}
	return glob, true
}

// Matches reports whether the rule applies to a request.
func (r RoutePriorityRule) Matches(method, path string) bool {
	if r.Method != "" && r.Method != method {
		return false
	}
	glob, ok := RoutePriorityGlob(r.Path)
	if !ok {
		return false
	}
	matched, err := MatchEdgeRulePath(glob, path)
	return err == nil && matched
}
