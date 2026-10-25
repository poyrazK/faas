package gregalemanifest

import (
	"fmt"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// PathRoute sends requests for a host and path to an app, declared in the
// manifest and reconciled with the deploy like async_routes
// (ADR-20261021223225). It becomes a manifest-owned kind=route edge rule on
// the target app and, with StripPrefix, a kind=rewrite rule that removes the
// prefix before the target sees the request.
type PathRoute struct {
	// Name is stable per target app; later deploys update the route with the
	// same name and remove manifest-owned routes that are no longer declared.
	Name      string `yaml:"name"`
	MatchHost string `yaml:"match_host"`
	// MatchPath is an edge-rule path glob, for example /api/**.
	MatchPath string `yaml:"match_path"`
	// To is the target app slug or, in a project deploy, its workload name.
	To string `yaml:"to"`
	// StripPrefix, when set, is removed from the path before forwarding:
	// /api/items reaches the target as /items.
	StripPrefix  string   `yaml:"strip_prefix,omitempty"`
	MatchMethods []string `yaml:"match_methods,omitempty"`
	Priority     *int     `yaml:"priority,omitempty"`
	Enabled      *bool    `yaml:"enabled,omitempty"`
}

var pathRouteMethods = map[string]bool{"GET": true, "HEAD": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true, "OPTIONS": true}

// Methods returns the route's methods, upper-cased and sorted. An empty
// list means every method.
func (r PathRoute) Methods() []string {
	methods := make([]string, 0, len(r.MatchMethods))
	for _, method := range r.MatchMethods {
		methods = append(methods, strings.ToUpper(strings.TrimSpace(method)))
	}
	sort.Strings(methods)
	return methods
}

// Validate checks one route declaration.
func (r PathRoute) Validate(index int) error {
	field := fmt.Sprintf("routes[%d]", index)
	if !isDNSSafeSlug(r.Name) {
		return fmt.Errorf("%s.name %q must match [a-z0-9-]+", field, r.Name)
	}
	if !isDNSSafeSlug(r.To) {
		return fmt.Errorf("%s.to %q must name an app or workload ([a-z0-9-]+)", field, r.To)
	}
	host := strings.TrimSpace(r.MatchHost)
	if host == "" || len(host) > 253 || strings.ContainsAny(host, "/ \t") {
		return fmt.Errorf("%s.match_host is required and must be a host name of at most 253 characters", field)
	}
	if !strings.HasPrefix(r.MatchPath, "/") || len(r.MatchPath) > 2048 {
		return fmt.Errorf("%s.match_path must start with '/' and be at most 2048 characters", field)
	}
	if glob, templated := api.EdgeRuleTemplatedPath(r.MatchPath); templated {
		return fmt.Errorf("%s.match_path %q is an OpenAPI template, not a glob; use %q", field, r.MatchPath, glob)
	}
	if r.StripPrefix != "" {
		if !strings.HasPrefix(r.StripPrefix, "/") || r.StripPrefix == "/" || strings.HasSuffix(r.StripPrefix, "/") || len(r.StripPrefix) > 1024 || strings.ContainsAny(r.StripPrefix, "*?[]{}") {
			return fmt.Errorf("%s.strip_prefix must be a literal path such as /api: a leading '/', no trailing '/', no glob characters", field)
		}
		if !strings.HasPrefix(r.MatchPath, r.StripPrefix) {
			return fmt.Errorf("%s.strip_prefix %q must be a prefix of match_path %q", field, r.StripPrefix, r.MatchPath)
		}
	}
	if r.Priority != nil && (*r.Priority < 0 || *r.Priority > 10000) {
		return fmt.Errorf("%s.priority must be in 0..10000", field)
	}
	seen := make(map[string]bool, len(r.MatchMethods))
	for _, method := range r.Methods() {
		if !pathRouteMethods[method] {
			return fmt.Errorf("%s.match_methods supports GET, HEAD, POST, PUT, PATCH, DELETE and OPTIONS (got %q)", field, method)
		}
		if seen[method] {
			return fmt.Errorf("%s.match_methods contains duplicate method %q", field, method)
		}
		seen[method] = true
	}
	return nil
}

// validatePathRoutes checks every declaration and rejects duplicate names
// per target and duplicate matches across the manifest: two routes for the
// same host, path and methods would race for the same requests.
func validatePathRoutes(routes []PathRoute) error {
	names := make(map[string]bool, len(routes))
	matches := make(map[string]int, len(routes))
	for i, route := range routes {
		if err := route.Validate(i); err != nil {
			return err
		}
		name := route.To + "\x00" + route.Name
		if names[name] {
			return fmt.Errorf("routes[%d]: duplicate name %q for app %q", i, route.Name, route.To)
		}
		names[name] = true
		match := strings.ToLower(strings.TrimSpace(route.MatchHost)) + "\x00" + route.MatchPath + "\x00" + strings.Join(route.Methods(), ",")
		if previous, exists := matches[match]; exists {
			return fmt.Errorf("routes[%d]: same match_host, match_path and methods as routes[%d]", i, previous)
		}
		matches[match] = i
	}
	return nil
}
