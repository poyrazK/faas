package openapidiff

// Generator extension for the auto-generated OpenAPI spec
// (ADR-126 / issue #975 item #2). The existing GenerateFromEdgeRules
// function at generator.go projects edge rules of kind="route"
// onto the platform spec. This file adds:
//
//  1. GenerateFromApp — the canonical entry point for the
//     `?source=auto` apid endpoint. It loads the imported doc
//     (state.Store.GetAppOpenAPIDoc), observed routes (via an
//     injected RoutesGetter — the ADR-093 gatewayd-internal
//     bridge), and existing edge rules (state.ListEdgeRulesForApp);
//     merges them into one Spec; returns the cache-key triple
//     (docSHA, routesSHA, rulesSHA) so the apid handler can
//     store + lookup the entry in the SpecCache.
//
//  2. AnnotateOperation — small helper that records the
//     `x-faas-edge-rules` extension in the apid-side sidecar
//     map keyed on (path, method). The pkg/openapidiff layer
//     stays schema-shape-only (the Operation struct doesn't
//     carry x-faas-* extensions), so the annotations are
//     surfaced via OpenAPIGenMeta per-operation.
//
//  3. ComputeDryRun — the read-only response shape for
//     `?dry-run`. Walks the imported doc's paths and emits a
//     EdgeRuleSuggestion per (path, method) pair NOT already
//     covered by an existing edge rule of matching kind. The
//     customer pastes each suggestion into the existing
//     create-edge-rule endpoint to apply.

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// GenerateFromAppInputs is the inputs bundle for GenerateFromApp.
// The fields are the loaders the function calls; nil values
// trigger ErrDegradedRoutes or ErrDegradedRules fallback paths
// (the apid handler reports them as Source: "degraded: …" rather
// than 502'ing).
type GenerateFromAppInputs struct {
	// AppID is the load-bearing cache key half.
	AppID string
	// AccountID scopes the Store reads (IDOR floor).
	AccountID string
	// ImportedDoc is the customer-uploaded OpenAPI bytes
	// (state.Store.GetAppOpenAPIDoc). Nil when no import exists.
	ImportedDoc []byte
	// ObservedRoutes is the ADR-093 observed-routes list
	// (gatewayd-internal GET /v1/internal/apps/{id}/routes).
	// Nil when the gateway is unavailable; GenerateFromApp
	// emits a degraded-but-still-useful spec in that case.
	ObservedRoutes []RouteRow
	// EdgeRules is the existing rules list for the app
	// (state.ListEdgeRulesForApp). Nil means the apid caller
	// failed to load the rules; the function falls back to
	// "no annotations".
	EdgeRules []state.EdgeRule
}

// RoutesGetter is the gateway-side read for ADR-093 observed
// routes. The interface is defined here so pkg/openapidiff
// stays daemon-neutral (it has no dependency on pkg/gateway).
// The apid handler passes an implementation that dials
// gatewayd-internal's loopback control listener.
//
// RouteRow is the per-route detail row returned by the gateway
// ADR-093 control listener. Re-aliased here from pkg/api so
// generator_ext callers don't need a second import for the
// type. The full pkg/api import is needed for the OpenAPI
// response DTOs that wrap the GenerateFromApp return value.
type RouteRow = api.RouteRow

// GenerateFromAppMeta is the metadata bundle returned alongside
// the generated spec. The apid handler packages this in the
// response's Inputs field so the dashboard can show "doc vN,
// routes vM, rules vK" — three independent change trackers.
//
// Annotations is the per-operation sidecar map keyed on
// "METHOD path" (e.g. "get /users/{id}"); the value is the
// list of (kind, action) pairs the apid layer must project
// onto the operation's `x-faas-edge-rules` extension when it
// re-renders the spec to JSON. pkg/openapidiff stays schema-
// shape-only — the sidecar is the bridge between this layer
// and the wire format.
type GenerateFromAppMeta struct {
	// DocSHA256 is sha256 of the imported doc bytes (raw, not
	// the jsonb-reserialised form). Empty when no import.
	DocSHA256 [32]byte
	// RoutesSHA256 is sha256 of the canonical (route+count)
	// rows concatenated. Empty when no observed routes.
	RoutesSHA256 [32]byte
	// RulesSHA256 is sha256 of the canonical (rule_id+kind+
	// action_sha) rows concatenated. Empty when no rules.
	RulesSHA256 [32]byte
	// Source is the source-string for the response (one of
	// "auto", "degraded: routes_unavailable", "degraded: rules_unavailable").
	Source string
	// Annotations is the per-operation sidecar: key is
	// "METHOD path" (e.g. "get /users/{id}"), value is the
	// list of (kind, action) pairs the operation enforces.
	// Nil when the apid handler doesn't need it (e.g., when
	// GenerateFromApp is called with no rules).
	Annotations map[string][]EdgeRuleProjection
}

// SourceAuto / SourceDegradedRoutes / SourceDegradedRules are
// the Source values the apid handler mirrors in
// pkg/appmetrics-style Source string. The dashboard surfaces
// these directly to the customer.
const (
	SourceAuto             = "auto"
	SourceDegradedRoutes   = "degraded: routes_unavailable"
	SourceDegradedRules    = "degraded: rules_unavailable"
	SourceDegradedImport   = "degraded: import_unavailable"
	SourceEmptyImport      = "empty: no_import"
	SourceEmptyImportRules = "empty: no_import_no_rules"
)

// ErrImportMissing is returned when the imported doc is nil/empty
// AND no edge rules exist for the app. The handler maps it to
// 200 with Source: "empty: no_import_no_rules" and an empty
// Paths map — the dashboard can render "no spec yet" rather
// than 404'ing.
var ErrImportMissing = errors.New("openapidiff: no imported doc and no edge rules")

// GenerateFromApp is the canonical entry point for the
// auto-generated spec. It assembles the three input streams
// (imported doc, observed routes, edge rules) and produces a
// *Spec with:
//
//   - paths from the imported doc (when present)
//   - stub path entries from ADR-093 observed routes
//     (paths the imported doc doesn't already cover, marked
//     "observed, not declared")
//   - `x-faas-edge-rules` annotations surfaced via the
//     returned meta.Annotations map so the apid handler can
//     re-render them on the wire without round-tripping
//
// The merged spec is the canonical input to the cache; the
// returned meta bundle is the cache-key triple + Source string
// the apid handler packages in the response.
func GenerateFromApp(in GenerateFromAppInputs) (*Spec, GenerateFromAppMeta, error) {
	meta := GenerateFromAppMeta{Source: SourceAuto}

	// Doc load.
	if len(in.ImportedDoc) > 0 {
		meta.DocSHA256 = sha256.Sum256(in.ImportedDoc)
	}

	// Rules load + degrade.
	rules := in.EdgeRules
	if rules == nil && len(in.ImportedDoc) == 0 && len(in.ObservedRoutes) == 0 {
		return nil, meta, ErrImportMissing
	}
	if rules == nil {
		meta.Source = SourceDegradedRules
	}

	// Spec assembly.
	spec := &Spec{
		Paths:      map[string]*PathItem{},
		Components: map[string]*Schema{},
	}

	// Layer 1: imported doc (paths + components).
	if len(in.ImportedDoc) > 0 {
		imported, err := LoadBytes(in.ImportedDoc)
		if err != nil {
			return nil, meta, fmt.Errorf("openapidiff: parse imported doc: %w", err)
		}
		for k, v := range imported.Paths {
			spec.Paths[k] = v
		}
		for k, v := range imported.Components {
			spec.Components[k] = v
		}
		spec.version = imported.version
	} else if meta.Source == SourceAuto {
		// No imported doc AND no observed errors: it's an
		// empty-but-degraded spec. The apid handler renders
		// this as Source: "empty: no_import".
		meta.Source = SourceEmptyImport
	}

	// Layer 2: observed routes — added as paths with a stub
	// operation if not already present. The ADR-093 row carries
	// "GET /users/4f8a" — split on the first space.
	if len(in.ObservedRoutes) > 0 {
		mergeObservedRoutes(spec, in.ObservedRoutes)
		meta.RoutesSHA256 = hashRoutes(in.ObservedRoutes)
	} else if meta.Source == SourceAuto {
		meta.Source = SourceDegradedRoutes
	}

	// Layer 3: edge rules — annotate each operation with the
	// (kind, action) list that applies.
	if rules != nil {
		meta.Annotations = annotateWithEdgeRules(spec, rules)
		meta.RulesSHA256 = hashRules(rules)
	}

	return spec, meta, nil
}

// mergeObservedRoutes walks the ADR-093 routes and adds a stub
// path entry per (method, path) pair the imported doc doesn't
// already cover. Stub operations carry a placeholder 200 response
// so the apid-side wire shape is valid JSON Schema; the apid
// handler tags them with `x-faas-observed-only: true` via the
// GenerateFromAppMeta sidecar.
func mergeObservedRoutes(spec *Spec, rows []RouteRow) {
	for _, row := range rows {
		// RouteRow.Route is "GET /users/4f8a" or "__route_other__".
		// We split on the first space.
		var method, path string
		for i := 0; i < len(row.Route); i++ {
			if row.Route[i] == ' ' {
				method = row.Route[:i]
				path = row.Route[i+1:]
				break
			}
		}
		if method == "" || path == "" {
			continue
		}
		method = lowerASCII(method)
		pi, ok := spec.Paths[path]
		if !ok {
			pi = &PathItem{Methods: map[string]*Operation{}}
			spec.Paths[path] = pi
		}
		if _, exists := pi.Methods[method]; exists {
			continue
		}
		op := &Operation{Responses: map[string]*Response{}}
		op.Responses["200"] = &Response{Content: map[string]*Schema{
			"application/json": {Type: "object"},
		}}
		pi.Methods[method] = op
	}
}

// annotateWithEdgeRules walks each edge rule and records the
// `x-faas-edge-rules` extension in the returned sidecar map
// keyed on "method path". The map is the bridge between the
// schema-shape-only Spec type and the wire-format
// x-faas-edge-rules extension — the apid handler joins the
// sidecar onto the JSON-marshalled spec at response time.
//
// Match logic: a rule with MatchHost="" and MatchPath="/v1/foo"
// matches any operation under the spec's paths["/v1/foo"]. A
// rule with MatchPath="/" matches every path. Host matching is
// ignored in v1 — the dashboard shows the rule actions verbatim
// regardless of host.
func annotateWithEdgeRules(spec *Spec, rules []state.EdgeRule) map[string][]EdgeRuleProjection {
	out := map[string][]EdgeRuleProjection{}
	for _, r := range rules {
		// Find the PathItem(s) the rule's path matches.
		var matchingPaths []string
		if r.MatchPath == "/" {
			for k := range spec.Paths {
				matchingPaths = append(matchingPaths, k)
			}
		} else {
			if _, ok := spec.Paths[r.MatchPath]; ok {
				matchingPaths = append(matchingPaths, r.MatchPath)
			}
		}
		for _, p := range matchingPaths {
			pi := spec.Paths[p]
			if pi == nil {
				continue
			}
			methods := r.MatchMethods
			if len(methods) == 0 {
				for m := range pi.Methods {
					methods = append(methods, m)
				}
			}
			for _, m := range methods {
				ml := lowerASCII(m)
				op, ok := pi.Methods[ml]
				if !ok {
					continue
				}
				_ = op // sidecar is the bridge, not op-mutation.
				key := ml + " " + p
				out[key] = append(out[key], EdgeRuleProjection{
					Kind:   string(r.Kind),
					Action: actionJSON(r.Action),
				})
			}
		}
	}
	return out
}

// EdgeRuleProjection is one (kind, action) pair an edge rule
// contributes to an Operation's x-faas-edge-rules annotation.
// Action is the JSON-serialised EdgeRuleAction struct (kind-
// tagged union); the apid handler marshals it back to JSON
// when projecting onto the wire.
type EdgeRuleProjection struct {
	Kind   string
	Action []byte
}

// AnnotateOperation is the documentation-hook for the
// apid-side sidecar. pkg/openapidiff stays schema-shape-only —
// the actual annotation write happens via the returned
// GenerateFromAppMeta.Annotations map. This function is a
// no-op marker for callers that want to invoke the
// annotation pipeline programmatically.
func AnnotateOperation(_ *Operation, _ EdgeRuleProjection) {
	// No-op: the sidecar is the bridge; see generator_ext.go's
	// package doc for the layering rationale.
}

// hashRules returns sha256 of a canonical concatenation of
// the (rule_id, kind, action) triples. Used as one half of the
// cache key (so a rules-only change invalidates the cache but
// a no-op rewrite is a cache hit).
func hashRules(rules []state.EdgeRule) [32]byte {
	sorted := make([]state.EdgeRule, len(rules))
	copy(sorted, rules)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	h := sha256.New()
	for _, r := range sorted {
		h.Write([]byte(r.ID))
		h.Write([]byte{0})
		h.Write([]byte(r.Kind))
		h.Write([]byte{0})
		h.Write(actionJSON(r.Action))
		h.Write([]byte{0})
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum
}

// hashRoutes returns sha256 of the canonical (route + count)
// row concatenation. Used as one half of the cache key for
// the observed-routes layer.
func hashRoutes(rows []RouteRow) [32]byte {
	sorted := make([]RouteRow, len(rows))
	copy(sorted, rows)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Route < sorted[j].Route })
	h := sha256.New()
	for _, r := range sorted {
		h.Write([]byte(r.Route))
		h.Write([]byte{0})
		// Count is a uint64; little-endian encoding is
		// stable across apid restarts.
		var countBuf [8]byte
		count := r.Count
		for i := 7; i >= 0; i-- {
			countBuf[i] = byte(count)
			count >>= 8
		}
		h.Write(countBuf[:])
		h.Write([]byte{0})
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum
}

// actionJSON marshals an EdgeRuleAction to its canonical JSON
// bytes for the cache key + sidecar wire form. Returns nil
// when the action is the zero value (no kind-specific payload).
func actionJSON(action state.EdgeRuleAction) []byte {
	// Use the existing struct JSON tags — they're the wire
	// contract the rest of the platform uses. Marshal directly
	// (no Encode/json.RawMessage wrap) so the bytes are stable
	// across apid restarts.
	b, err := json.Marshal(action)
	if err != nil {
		return nil
	}
	return b
}

// DryRunSuggestions is the read-only response shape for the
// `?dry-run` endpoint. Walks the imported doc's paths and
// emits one EdgeRuleSuggestion per (path, method) pair NOT
// already covered by an existing edge rule of matching kind.
// The customer pastes each suggestion into the existing
// create-edge-rule endpoint.
type DryRunSuggestions struct {
	// Suggestions is one row per (path, method) pair that has
	// no existing edge rule. Empty when fully covered.
	Suggestions []EdgeRuleSuggestion
	// OpenAPIVersion is the imported doc's declared version
	// ("3.1.0", etc.). Empty when no import.
	OpenAPIVersion string
	// EndpointCount is the count of operations the imported
	// doc declares (from the openapiimport validator's count).
	EndpointCount int
}

// EdgeRuleSuggestion is one row of the dry-run response. Mirrors
// the shape of api.CreateEdgeRuleRequest minus the audit/ID
// fields; the customer pastes Path + Methods + Kind + Action
// back into the create-edge-rule endpoint.
type EdgeRuleSuggestion struct {
	Path    string         `json:"path"`
	Methods []string       `json:"methods"`
	Kind    string         `json:"kind"`
	Action  map[string]any `json:"action"`
}

// ComputeDryRun walks the imported doc and emits one
// EdgeRuleSuggestion per (path, method) pair NOT already
// covered by an existing edge rule of matching kind. The
// suggestions carry a default `kind="validate"` action (the
// safest default — the customer can edit before applying).
func ComputeDryRun(importedDoc []byte, existingRules []state.EdgeRule) (DryRunSuggestions, error) {
	var out DryRunSuggestions
	if len(importedDoc) == 0 {
		return out, nil
	}
	spec, err := LoadBytes(importedDoc)
	if err != nil {
		return out, fmt.Errorf("openapidiff: parse imported doc for dry-run: %w", err)
	}
	out.OpenAPIVersion = spec.OpenAPIVersion()
	// Build the existing-rules set keyed on (path, method, kind).
	existing := map[string]struct{}{}
	for _, r := range existingRules {
		for _, m := range r.MatchMethods {
			existing[r.MatchPath+"|"+lowerASCII(m)+"|"+string(r.Kind)] = struct{}{}
		}
	}
	// Walk each (path, method) in the spec.
	for path, pi := range spec.Paths {
		methodList := make([]string, 0, len(pi.Methods))
		for m := range pi.Methods {
			methodList = append(methodList, m)
		}
		sort.Strings(methodList)
		for _, m := range methodList {
			if _, covered := existing[path+"|"+m+"|validate"]; covered {
				continue
			}
			out.Suggestions = append(out.Suggestions, EdgeRuleSuggestion{
				Path:    path,
				Methods: []string{m},
				Kind:    "validate",
				Action: map[string]any{
					"kind": "validate",
					"validate": map[string]any{
						"schema":        map[string]any{"type": "object"},
						"validate_mode": "observe",
					},
				},
			})
		}
		out.EndpointCount += len(methodList)
	}
	sort.Slice(out.Suggestions, func(i, j int) bool {
		if out.Suggestions[i].Path != out.Suggestions[j].Path {
			return out.Suggestions[i].Path < out.Suggestions[j].Path
		}
		return out.Suggestions[i].Methods[0] < out.Suggestions[j].Methods[0]
	})
	return out, nil
}

// lowerASCII is a small helper that lowercases a string
// without allocating for the ASCII-fast path. Method names
// are HTTP-verbs which are ASCII, so the fast path is the
// common case.
func lowerASCII(s string) string {
	out := []byte(s)
	for i, c := range out {
		if c >= 'A' && c <= 'Z' {
			out[i] = c + ('a' - 'A')
		}
	}
	return string(out)
}
