package flags

import (
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// Edge routes a variant flag's variants at the gateway, so an app without an
// SDK (a static site, a route the code does not branch on) still serves them
// (ADR-20261124212701). The gateway evaluates the flag for each request that
// matches a route and, for the decided variant, rewrites the request path
// before the response cache and the app see it.
type Edge struct {
	Routes []EdgeRoute `json:"routes"`
}

// EdgeRoute is one routed path: "/pricing" matches exactly, "/docs/*"
// matches /docs and everything under it. Variants without an action serve
// the request unchanged.
type EdgeRoute struct {
	Path     string                `json:"path"`
	Variants map[string]EdgeAction `json:"variants"`
}

// EdgeAction is what a variant does to a matching request.
type EdgeAction struct {
	// Rewrite replaces the matched path: the whole path for an exact route,
	// the prefix for a "/*" route (/docs/a with /docs/* -> /docs-b/a).
	Rewrite string `json:"rewrite"`
}

// edgePrefix returns a prefix route's prefix and true, or the exact path and
// false.
func edgePrefix(path string) (string, bool) {
	if prefix, ok := strings.CutSuffix(path, "/*"); ok {
		return prefix, true
	}
	return path, false
}

func validEdgePath(path string, route bool) bool {
	if path == "" || path[0] != '/' || len(path) > api.FlagsMaxEdgePathBytes || strings.ContainsAny(path, "?#\\\x00") {
		return false
	}
	if route {
		path, _ = edgePrefix(path)
		if path == "" {
			// "/*" routes everything: allowed, the prefix is the root.
			return true
		}
	}
	if strings.Contains(path, "*") || strings.Contains(path, "//") {
		return false
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "." || segment == ".." {
			return false
		}
	}
	for _, r := range path {
		if r < 0x21 || r == 0x7f {
			return false
		}
	}
	return true
}

// validateEdge checks the edge routes of every flag: variant flags only,
// bounded, valid paths and known variants, no path routed twice.
func validateEdge(c Config) error {
	edgeFlags := 0
	paths := map[string]string{}
	for _, f := range c.Flags {
		if f.Edge == nil {
			continue
		}
		edgeFlags++
		if flagType(f) != "variant" {
			return fmt.Errorf("flag %q: only variant flags route at the edge", f.Key)
		}
		if len(f.Edge.Routes) == 0 || len(f.Edge.Routes) > api.FlagsMaxEdgeRoutesPerFlag {
			return fmt.Errorf("flag %q: edge routing needs 1 to %d routes", f.Key, api.FlagsMaxEdgeRoutesPerFlag)
		}
		variants := map[string]bool{}
		for _, v := range f.Variants {
			variants[v.Key] = true
		}
		for _, route := range f.Edge.Routes {
			if !validEdgePath(route.Path, true) {
				return fmt.Errorf("flag %q: invalid edge path %q: use /path or /prefix/* without query, fragment or dot segments", f.Key, route.Path)
			}
			if owner, taken := paths[route.Path]; taken {
				return fmt.Errorf("edge path %q is routed by both %q and %q", route.Path, owner, f.Key)
			}
			paths[route.Path] = f.Key
			if len(route.Variants) == 0 {
				return fmt.Errorf("flag %q: edge path %q routes no variant", f.Key, route.Path)
			}
			for variant, action := range route.Variants {
				if !variants[variant] {
					return fmt.Errorf("flag %q: edge path %q names unknown variant %q", f.Key, route.Path, variant)
				}
				if !validEdgePath(action.Rewrite, false) {
					return fmt.Errorf("flag %q: edge rewrite %q for variant %q must be a path without query, fragment, wildcards or dot segments", f.Key, action.Rewrite, variant)
				}
			}
		}
	}
	if edgeFlags > api.FlagsMaxEdgeFlags {
		return fmt.Errorf("at most %d flags may route at the edge (%d do)", api.FlagsMaxEdgeFlags, edgeFlags)
	}
	return nil
}

// SameEdge reports whether a and b route the same paths the same way, flag
// by flag: an edit that leaves edge routes alone keeps publishing after the
// edge_flags platform flag is turned off.
func SameEdge(a, b Config) bool {
	routes := func(c Config) map[string]Edge {
		out := map[string]Edge{}
		for _, f := range c.Flags {
			if f.Edge != nil {
				out[f.Key] = *f.Edge
			}
		}
		return out
	}
	return reflect.DeepEqual(routes(a), routes(b))
}

// HasEdge reports whether any flag of c routes at the edge.
func HasEdge(c Config) bool {
	return slices.ContainsFunc(c.Flags, func(f Flag) bool { return f.Edge != nil })
}

// WithoutEdge returns c with every flag's edge routes removed: the runtime
// bundle SDKs read never carries them.
func WithoutEdge(c Config) Config {
	if !HasEdge(c) {
		return c
	}
	out := c
	out.Flags = slices.Clone(c.Flags)
	for i := range out.Flags {
		out.Flags[i].Edge = nil
	}
	return out
}

// EdgeOnly returns the edge-routed flags of c, for the gateway's cache.
func EdgeOnly(c Config) Config {
	out := Config{Groups: c.Groups, Holdout: c.Holdout, Layers: c.Layers}
	for _, f := range c.Flags {
		if f.Edge != nil {
			out.Flags = append(out.Flags, f)
		}
	}
	return out
}

// EdgeMatch returns the edge-routed flag and route for path: an exact route
// first, then the longest matching prefix route.
func EdgeMatch(c Config, path string) (Flag, EdgeRoute, bool) {
	var (
		best      Flag
		bestRoute EdgeRoute
		bestLen   = -1
	)
	for _, f := range c.Flags {
		if f.Edge == nil {
			continue
		}
		for _, route := range f.Edge.Routes {
			prefix, isPrefix := edgePrefix(route.Path)
			if !isPrefix {
				if path == route.Path {
					return f, route, true
				}
				continue
			}
			if (path == prefix || strings.HasPrefix(path, prefix+"/") || prefix == "") && len(prefix) > bestLen {
				best, bestRoute, bestLen = f, route, len(prefix)
			}
		}
	}
	return best, bestRoute, bestLen >= 0
}

// EdgeRewritePath applies a variant's action to path, which matched route.
func EdgeRewritePath(route EdgeRoute, action EdgeAction, path string) string {
	prefix, isPrefix := edgePrefix(route.Path)
	if !isPrefix {
		return action.Rewrite
	}
	rest := strings.TrimPrefix(path, prefix)
	target := strings.TrimSuffix(action.Rewrite, "/")
	if rest == "" {
		if target == "" {
			return "/"
		}
		return target
	}
	return target + rest
}
