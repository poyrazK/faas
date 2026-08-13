package openapidiff

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// GenerateFromEdgeRules projects a manifest's edge-rule list
// onto the embedded OpenAPI spec, returning a new [Spec] that
// includes any new route paths the manifest declares. The
// baseline spec (the [base] argument) is treated as the
// authoritative source for all paths the embedded file
// already declares; this function adds route entries on top.
//
// Scope (PR-2 v1):
//
//   - Edge rules of kind="route" contribute one OpenAPI path
//     entry. MatchHost + MatchPath combine into the OpenAPI
//     path key. The HTTP method set is the union of the rule's
//     MatchMethods list (defaults to [GET] when the rule
//     declares none). The response schema is a narrow stub
//     — {type: object, properties: {status: {type: integer}}}
//     — sufficient to give the differ a typed anchor without
//     overfitting to a customer's actual handler signature.
//
//   - Edge rules of kind="rewrite", "redirect", "headers",
//     "cors", "jwt", "ip" do NOT generate path entries. They
//     modify the response shape of an existing path rather
//     than adding a new one, and PR-2's differ does not yet
//     walk into response-shape-mutation rules. That work is
//     explicitly out of scope for the v1 cluster.
//
// Why a path-addition-only v1: the customer's app-specific
// OpenAPI is not yet a customer input — the OpenAPI 3.1 spec
// the customer ships today is the platform spec, not their
// app. Adding a route path the manifest declares is the
// highest-precision structural signal we can emit without
// that input; type changes on existing paths require
// customer-supplied app schemas (tracked as a follow-on).
//
// base may be nil — the function then loads the embedded
// spec via [Load]. errors from Load propagate.
func GenerateFromEdgeRules(base *Spec, baseRules, pendingRules []api.CreateEdgeRuleRequest) (*Spec, error) {
	if base == nil {
		var err error
		base, err = Load()
		if err != nil {
			return nil, fmt.Errorf("openapidiff: load embedded: %w", err)
		}
	}
	// Shallow-copy the spec so we don't mutate the caller's
	// embedded spec. Maps are shallow-copied; PathItem.Methods
	// maps are deep-copied because we may add to them.
	out := &Spec{
		version:    base.version,
		Paths:      map[string]*PathItem{},
		Components: map[string]*Schema{},
	}
	for k, v := range base.Paths {
		out.Paths[k] = shallowCopyPathItem(v)
	}
	for k, v := range base.Components {
		out.Components[k] = shallowCopySchema(v)
	}
	// Project pending edge rules of kind="route" onto the spec.
	// We walk the pending list once, dedup on (host, path,
	// methods) so a manifest with two rules for the same host
	// + path with different methods produces ONE path entry
	// with the union of methods.
	type routeKey struct {
		host, path string
	}
	methodsByKey := map[routeKey]map[string]struct{}{}
	keyOrder := []routeKey{}
	for _, r := range pendingRules {
		if !strings.EqualFold(r.Kind, "route") {
			continue
		}
		// A route rule without an explicit method set defaults
		// to GET — matches gatewayd's compiled-rule behaviour
		// when MatchMethods is empty.
		methods := r.MatchMethods
		if len(methods) == 0 {
			methods = []string{http.MethodGet}
		}
		// Normalise: lower-case, sort, dedup.
		set := map[string]struct{}{}
		for _, m := range methods {
			set[strings.ToLower(m)] = struct{}{}
		}
		k := routeKey{host: r.MatchHost, path: r.MatchPath}
		if _, exists := methodsByKey[k]; !exists {
			keyOrder = append(keyOrder, k)
		}
		// Union this rule's methods onto whatever previous rules
		// for the same (host, path) declared. Two rules for
		// /v1/foo with GET and POST must project one path entry
		// with both methods — duplicating the path key in the
		// OpenAPI output would cause Compare to flag a phantom
		// "path added + path removed" pair.
		if existing, ok := methodsByKey[k]; ok {
			for m := range set {
				existing[m] = struct{}{}
			}
			continue
		}
		methodsByKey[k] = set
	}
	// Stable order: sort by (host, path).
	sort.Slice(keyOrder, func(i, j int) bool {
		if keyOrder[i].host != keyOrder[j].host {
			return keyOrder[i].host < keyOrder[j].host
		}
		return keyOrder[i].path < keyOrder[j].path
	})
	for _, k := range keyOrder {
		openPath := openAPIPathFor(k.host, k.path)
		methodSet := methodsByKey[k]
		// Skip if the path is already in the embedded spec —
		// those are platform-managed paths, not app-managed.
		if _, exists := out.Paths[openPath]; exists {
			continue
		}
		pi := &PathItem{Methods: map[string]*Operation{}}
		// Stable method order.
		methodList := make([]string, 0, len(methodSet))
		for m := range methodSet {
			methodList = append(methodList, m)
		}
		sort.Strings(methodList)
		for _, m := range methodList {
			pi.Methods[m] = defaultOperation()
		}
		out.Paths[openPath] = pi
	}
	// Removed route paths: any path that the baseline edge
	// rules contributed but the pending list doesn't. PR-2
	// walks the baseline edge-rule list (passed in as
	// baseRules) to discover paths the *previous* deploy
	// added. We then remove those paths from out when they
	// are absent from the pending list.
	removedKeys := map[routeKey]struct{}{}
	for _, r := range baseRules {
		if !strings.EqualFold(r.Kind, "route") {
			continue
		}
		k := routeKey{host: r.MatchHost, path: r.MatchPath}
		// Was this path still in the pending list?
		if _, stillPresent := methodsByKey[k]; stillPresent {
			continue
		}
		removedKeys[k] = struct{}{}
	}
	for k := range removedKeys {
		openPath := openAPIPathFor(k.host, k.path)
		delete(out.Paths, openPath)
	}
	return out, nil
}

// openAPIPathFor maps a (host, path) edge-rule tuple to the
// OpenAPI path key the differ walks. The current rule schema
// stores paths with a leading "/" (e.g. "/v1/foo"); the host
// is part of the routing decision but the OpenAPI key is the
// path only — the host becomes the server name in a future
// multi-tenant OpenAPI. For PR-2 v1 the differ treats each
// (host, path) as its own OpenAPI path key by prefixing the
// host, e.g. "api.example.com/v1/foo". This keeps the differ's
// output deterministic without conflating distinct customers'
// routes.
func openAPIPathFor(host, path string) string {
	if host == "" {
		return path
	}
	if path == "" {
		return host
	}
	return host + path
}

// defaultOperation returns a stub [Operation] for a route-edge-rule
// projected path. The stub is the minimum the differ needs:
// a 200 response with a generic JSON object schema. The shape
// is intentionally generic — the customer's actual handler
// schema is unknown to us, so we project only what the differ
// can walk: response exists, response is JSON, response is an
// object. Adding more would require customer-supplied app
// OpenAPI files, which PR-2 v1 doesn't have.
func defaultOperation() *Operation {
	op := &Operation{Responses: map[string]*Response{}}
	op.Responses["200"] = &Response{Content: map[string]*Schema{
		"application/json": {
			Type: "object",
			Properties: map[string]*Schema{
				"status": {Type: "integer"},
			},
			Required: []string{"status"},
		},
	}}
	return op
}

// shallowCopyPathItem returns a fresh [PathItem] with the same
// Methods map (entries are pointer-shared — callers must not
// mutate them).
func shallowCopyPathItem(in *PathItem) *PathItem {
	if in == nil {
		return nil
	}
	out := &PathItem{Methods: map[string]*Operation{}}
	for k, v := range in.Methods {
		out.Methods[k] = v
	}
	return out
}

// shallowCopySchema returns a fresh [Schema] with the same
// fields. Properties / Items / OneOf / AnyOf entries are
// pointer-shared — callers must not mutate them.
func shallowCopySchema(in *Schema) *Schema {
	if in == nil {
		return nil
	}
	out := &Schema{
		Type:        in.Type,
		Properties:  in.Properties,
		Required:    append([]string(nil), in.Required...),
		Items:       in.Items,
		Nullable:    in.Nullable,
		OneOf:       in.OneOf,
		AnyOf:       in.AnyOf,
		Ref:         in.Ref,
		Description: in.Description,
	}
	return out
}
