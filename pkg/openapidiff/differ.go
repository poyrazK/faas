package openapidiff

import (
	"reflect"
	"sort"
	"strings"
)

// Compare walks two [Spec]s and emits one [SchemaBreak] per
// observed structural change. The differ is symmetric: identical
// inputs produce zero breaks; every break is a "baseline → proposed"
// delta the engine surfaces as a customer-facing message.
//
// The walk covers paths × methods × responses × content type
// schemas. Within each schema the differ recursively compares:
//
//  1. Type — string/object/integer/array/null. A type flip is the
//     most common wire break and the strongest signal.
//
//  2. Properties — added / removed. Added properties are NOT a
//     break (clients tolerate unknown fields by JSON Schema
//     convention), removed properties ARE a break (clients
//     expecting them will get null/undefined).
//
//  3. Required response guarantees — adding or removing a guarantee changes
//     the response shape expected by generated clients.
//
//  4. Nullability — flips. `nullable: true` ⇨ false (or vice
//     versa) on the schema itself, or on a property. The OpenAPI
//     3.1 `[T, 'null']` form is treated as `nullable: true` per
//     memory [pr-819-openapi-nullable-3-1] — the noise rule
//     guarantees [Load] collapses both forms to the same
//     [Schema.Nullable] value, so the differ never sees the
//     array form.
//
//  5. Items (array element type) — same rules as Type, applied
//     recursively. PR-2 does not diff Items' own Properties —
//     a JSON Schema array whose items are objects is unusual in
//     our REST surface, and the differ's recursion captures the
//     common "array of string" case.
//
// Property order and description whitespace are noise: the loader
// normalises both, so the differ never sees them as a difference.
//
// OneOf / AnyOf unions and unsupported raw schema facets are not classified
// as breaking or additive. Changes emit unknown findings rather than being
// silently treated as compatible or misreported as confirmed breaks.
// Compare is the legacy break-only view; callers making compatibility
// decisions should use [CompareDetailed] so they cannot drop unknowns.
func Compare(baseline, proposed *Spec) []SchemaBreak {
	return CompareDetailed(baseline, proposed).Breaks
}

// CompareDetailed compares the supported response-schema facets and reports
// changes to unsupported schema features separately from confirmed breaking
// changes. Callers deciding whether to allow a promotion must inspect both
// slices.
func CompareDetailed(baseline, proposed *Spec) SchemaComparison {
	if baseline == nil || proposed == nil {
		return SchemaComparison{}
	}
	var result SchemaComparison
	// Sort path keys for deterministic output (the engine sorts
	// its own emit too, but sorting here keeps Compare pure).
	pathKeys := unionSortedKeys(baseline.Paths, proposed.Paths)
	for _, pathKey := range pathKeys {
		basePI := baseline.Paths[pathKey]
		propPI := proposed.Paths[pathKey]
		// A path in proposed but not in baseline = NEW endpoint.
		// The engine treats new endpoints as a *positive* change,
		// not a break — clients discover the new path via the
		// response headers. PR-2 therefore emits no break for
		// path adds; the engine still surfaces the path in
		// Diff.Changes via its edge-rule walker.
		if basePI == nil {
			continue
		}
		if propPI == nil {
			// Path removed in proposed → every method's responses
			// are now missing. One break per method.
			methodKeys := unionSortedKeys(basePI.Methods)
			for _, method := range methodKeys {
				result.Breaks = append(result.Breaks, SchemaBreak{
					Path: pathKey, Method: method, Status: "",
					Kind:   SchemaKindFieldRemoved,
					Before: pathKey,
				})
			}
			continue
		}
		// Both present — walk methods.
		methodKeys := unionSortedKeys(basePI.Methods, propPI.Methods)
		for _, method := range methodKeys {
			baseOp := basePI.Methods[method]
			propOp := propPI.Methods[method]
			if baseOp == nil {
				// Method added — not a break.
				continue
			}
			if propOp == nil {
				result.Breaks = append(result.Breaks, SchemaBreak{
					Path: pathKey, Method: method, Status: "",
					Kind:   SchemaKindFieldRemoved,
					Before: method,
				})
				continue
			}
			// Both present — walk responses.
			statusKeys := unionSortedKeys(baseOp.Responses, propOp.Responses)
			for _, status := range statusKeys {
				baseResp := baseOp.Responses[status]
				propResp := propOp.Responses[status]
				if baseResp == nil {
					continue
				}
				if propResp == nil {
					// Status removed — every content type's schema
					// is gone. One break.
					result.Breaks = append(result.Breaks, SchemaBreak{
						Path: pathKey, Method: method, Status: status,
						Kind:   SchemaKindFieldRemoved,
						Before: status,
					})
					continue
				}
				ctKeys := unionSortedKeys(baseResp.Content, propResp.Content)
				for _, ct := range ctKeys {
					baseSch := baseResp.Content[ct]
					propSch := propResp.Content[ct]
					if baseSch == nil {
						continue
					}
					if propSch == nil {
						result.Breaks = append(result.Breaks, SchemaBreak{
							Path: pathKey, Method: method, Status: status,
							Kind:         SchemaKindFieldRemoved,
							PathInSchema: ct,
							Before:       ct,
						})
						continue
					}
					comparison := diffSchema(pathKey, method, status, ct, baseSch, propSch, baseline, proposed)
					result.Breaks = append(result.Breaks, comparison.Breaks...)
					result.Unknowns = append(result.Unknowns, comparison.Unknowns...)
				}
			}
		}
	}
	sort.Slice(result.Unknowns, func(i, j int) bool {
		a, b := result.Unknowns[i], result.Unknowns[j]
		return strings.Join([]string{a.Path, a.Method, a.Status, a.PathInSchema, string(a.Code)}, "\x00") <
			strings.Join([]string{b.Path, b.Method, b.Status, b.PathInSchema, string(b.Code)}, "\x00")
	})
	return result
}

// diffSchema recursively compares two schemas at a known
// path/method/status/content-type location. Returns a slice
// of SchemaBreaks anchored to the same Path/Method/Status —
// the caller appends.
//
// The recursion walks Properties and Items. Refs are resolved
// against the parent Specs (each spec has its own Components
// map, so a $ref may resolve differently on each side — the
// differ treats that as a structural break too).
func diffSchema(path, method, status, ct string, base, prop *Schema, baseSpec, propSpec *Spec) SchemaComparison {
	return diffSchemaDepth(path, method, status, ct, base, prop, baseSpec, propSpec, 0)
}

// diffSchemaDepth is the depth-bounded variant of diffSchema.
// The OpenAPI schema graph is allowed to be cyclic (FilterCriteria
// self-references for its $or/$and/payload branches; the schema
// loader survives the cycle, but the recursive differ does not).
// depth 0 starts at the initial node; depth 8 mirrors resolveRef's
// maxDepth and any deeper walk returns no further breaks (the
// already-recorded break at the cycle edge is what the user reads).
func diffSchemaDepth(path, method, status, ct string, base, prop *Schema, baseSpec, propSpec *Spec, depth int) SchemaComparison {
	const maxDepth = 8
	if depth >= maxDepth {
		return SchemaComparison{}
	}
	_ = ct // ct is already encoded in the breaks; kept in the signature for readability.
	var result SchemaComparison
	// $ref resolution. baseSpec / propSpec own their own
	// Components; we resolve each side independently. When
	// the resolved schemas differ the recursion handles it.
	base = resolveRef(base, baseSpec)
	prop = resolveRef(prop, propSpec)
	// 1. Type change.
	if base.Type != prop.Type {
		// Treat empty (union) as "no type change" — the differ
		// does not walk unions.
		if base.Type != "" && prop.Type != "" {
			result.Breaks = append(result.Breaks, SchemaBreak{
				Path: path, Method: method, Status: status,
				Kind:   SchemaKindTypeChange,
				Before: base.Type, After: prop.Type,
			})
		}
	}
	// 2. Nullability change.
	if base.Nullable != prop.Nullable {
		result.Breaks = append(result.Breaks, SchemaBreak{
			Path: path, Method: method, Status: status,
			Kind:         SchemaKindNullabilityChange,
			PathInSchema: "",
			Before:       base.Nullable, After: prop.Nullable,
		})
	}
	// 3. Removed properties.
	for name := range base.Properties {
		if _, ok := prop.Properties[name]; !ok {
			result.Breaks = append(result.Breaks, SchemaBreak{
				Path: path, Method: method, Status: status,
				Kind:         SchemaKindFieldRemoved,
				PathInSchema: "properties." + name,
				Before:       name,
			})
		}
	}
	// 4. Required response properties. A field becoming optional removes
	// a guarantee clients may rely on. Skip missing fields because they
	// already receive the more specific field_removed finding above.
	baseReq := keySetString(base.Required)
	propReq := keySetString(prop.Required)
	for _, name := range prop.Required {
		if _, ok := baseReq[name]; !ok {
			result.Breaks = append(result.Breaks, SchemaBreak{
				Path: path, Method: method, Status: status,
				Kind:         SchemaKindRequiredAdded,
				PathInSchema: "properties." + name,
				After:        name,
			})
		}
	}
	for _, name := range base.Required {
		if _, stillRequired := propReq[name]; stillRequired {
			continue
		}
		if _, stillPresent := prop.Properties[name]; !stillPresent {
			continue
		}
		result.Breaks = append(result.Breaks, SchemaBreak{
			Path: path, Method: method, Status: status,
			Kind:         SchemaKindRequiredRemoved,
			PathInSchema: "properties." + name,
			Before:       name,
		})
	}
	// Unsupported union changes cannot yet be classified as breaking or
	// additive. Other unsupported schema facets (enum, format, constraints,
	// composition, and so on) receive the same treatment at their own node.
	baselineIncompleteAtNode := false
	if hasSchemaUnion(base, prop) {
		if code := schemaUnionUnknown(base, prop, baseSpec, propSpec); code != "" {
			result.Unknowns = append(result.Unknowns, SchemaUnknown{
				Path: path, Method: method, Status: status,
				Code: code,
			})
		}
	} else if code := schemaUnsupportedFacetUnknown(base, prop); code != "" {
		baselineIncompleteAtNode = code == SchemaUnknownSchemaBaselineIncomplete
		result.Unknowns = append(result.Unknowns, SchemaUnknown{
			Path: path, Method: method, Status: status,
			Code: code,
		})
	}
	// 5. Recurse into shared properties + Items.
	for name, baseChild := range base.Properties {
		if propChild, ok := prop.Properties[name]; ok {
			childPathInSchema := "properties." + name
			child := diffSchemaDepth(path, method, status, ct, baseChild, propChild, baseSpec, propSpec, depth+1)
			for _, b := range child.Breaks {
				b.PathInSchema = joinPath(b.PathInSchema, childPathInSchema)
				result.Breaks = append(result.Breaks, b)
			}
			for _, unknown := range child.Unknowns {
				if baselineIncompleteAtNode && unknown.Code == SchemaUnknownSchemaBaselineIncomplete {
					continue
				}
				unknown.PathInSchema = joinPath(unknown.PathInSchema, childPathInSchema)
				result.Unknowns = append(result.Unknowns, unknown)
			}
		}
	}
	if base.Items != nil && prop.Items != nil {
		child := diffSchemaDepth(path, method, status, ct, base.Items, prop.Items, baseSpec, propSpec, depth+1)
		for _, b := range child.Breaks {
			b.PathInSchema = joinPath(b.PathInSchema, "items")
			result.Breaks = append(result.Breaks, b)
		}
		for _, unknown := range child.Unknowns {
			if baselineIncompleteAtNode && unknown.Code == SchemaUnknownSchemaBaselineIncomplete {
				continue
			}
			unknown.PathInSchema = joinPath(unknown.PathInSchema, "items")
			result.Unknowns = append(result.Unknowns, unknown)
		}
	}
	// Avoid an "unused variable" warning when propReq is
	// only consulted in the loop above.
	_ = propReq
	return result
}

// schemaUnionUnknown reports whether either union kind or any alternative's
// observable schema shape changed. Union order is ignored because oneOf and
// anyOf alternative order has no validation meaning. Raw facets outside the
// structural differ are compared inside alternatives so an enum, format, or
// discriminator edit is not presented as a clean comparison. A snapshot
// without the opaque facet marker is incomplete when it contains a union.
func schemaUnionUnknown(base, prop *Schema, baseSpec, propSpec *Spec) SchemaUnknownCode {
	if base == nil || prop == nil {
		if base != prop {
			return SchemaUnknownUnsupportedUnionChange
		}
		return ""
	}
	if !hasSchemaUnion(base, prop) {
		return ""
	}
	seen := make(map[schemaRefPair]bool)
	if (len(base.OneOf) > 0 || len(prop.OneOf) > 0) && !schemaAlternativesEqual(base.OneOf, prop.OneOf, baseSpec, propSpec, seen) {
		return SchemaUnknownUnsupportedUnionChange
	}
	if (len(base.AnyOf) > 0 || len(prop.AnyOf) > 0) && !schemaAlternativesEqual(base.AnyOf, prop.AnyOf, baseSpec, propSpec, seen) {
		return SchemaUnknownUnsupportedUnionChange
	}
	if !reflect.DeepEqual(schemaUnsupportedFacets(base), schemaUnsupportedFacets(prop)) {
		return SchemaUnknownUnsupportedUnionChange
	}
	if base.UnsupportedFacetsSHA256 != "" && prop.UnsupportedFacetsSHA256 != "" &&
		base.UnsupportedFacetsSHA256 != prop.UnsupportedFacetsSHA256 {
		return SchemaUnknownUnsupportedUnionChange
	}
	if base.UnsupportedFacetsSHA256 == "" || prop.UnsupportedFacetsSHA256 == "" {
		return SchemaUnknownUnionBaselineIncomplete
	}
	return ""
}

func hasSchemaUnion(base, prop *Schema) bool {
	return base != nil && prop != nil &&
		(len(base.OneOf) > 0 || len(base.AnyOf) > 0 || len(prop.OneOf) > 0 || len(prop.AnyOf) > 0)
}

func schemaUnsupportedFacetUnknown(base, prop *Schema) SchemaUnknownCode {
	if base == nil || prop == nil {
		return ""
	}
	// Normal loaded and current snapshots have fingerprints. This is the
	// reliable path: raw facet values remain absent from persisted reports.
	if base.UnsupportedFacetsSHA256 != "" && prop.UnsupportedFacetsSHA256 != "" {
		if base.UnsupportedFacetsSHA256 != prop.UnsupportedFacetsSHA256 {
			return SchemaUnknownUnsupportedSchemaChange
		}
		return ""
	}

	baseFacets, propFacets := schemaUnsupportedFacets(base), schemaUnsupportedFacets(prop)
	// Hand-built Specs and live LoadBytes results can still be compared from
	// their raw maps when both sides retain them.
	if base.Raw != nil && prop.Raw != nil {
		if !reflect.DeepEqual(baseFacets, propFacets) {
			return SchemaUnknownUnsupportedSchemaChange
		}
		return ""
	}

	// A snapshot with neither raw values nor a fingerprint cannot prove that
	// unsupported facets were absent. Report incompleteness even when the
	// proposed schema has no such facets, since they may have been removed.
	return SchemaUnknownSchemaBaselineIncomplete
}

func schemaAlternativesEqual(base, prop []*Schema, baseSpec, propSpec *Spec, seen map[schemaRefPair]bool) bool {
	if len(base) != len(prop) {
		return false
	}
	matched := make([]bool, len(prop))
	for _, baseSchema := range base {
		found := false
		for i, propSchema := range prop {
			if matched[i] || !schemaEquivalent(baseSchema, propSchema, baseSpec, propSpec, 0, cloneSchemaRefPairs(seen)) {
				continue
			}
			matched[i] = true
			found = true
			break
		}
		if !found {
			return false
		}
	}
	return true
}

type schemaRefPair struct{ baseline, proposed string }

func schemaEquivalent(base, prop *Schema, baseSpec, propSpec *Spec, depth int, seen map[schemaRefPair]bool) bool {
	const maxComparisonDepth = 16
	if base == nil || prop == nil {
		return base == prop
	}
	if depth >= maxComparisonDepth {
		return false // uncertainty at the containing union is safer than a clean result.
	}
	if base.Ref != "" || prop.Ref != "" {
		pair := schemaRefPair{baseline: base.Ref, proposed: prop.Ref}
		if seen[pair] {
			return true
		}
		seen[pair] = true
	}
	base = resolveRef(base, baseSpec)
	prop = resolveRef(prop, propSpec)
	if base == nil || prop == nil {
		return base == prop
	}
	if base.Type != prop.Type || base.Nullable != prop.Nullable || base.Ref != prop.Ref ||
		!equalSchemaStrings(base.Required, prop.Required) ||
		(base.UnsupportedFacetsSHA256 != "" && prop.UnsupportedFacetsSHA256 != "" &&
			base.UnsupportedFacetsSHA256 != prop.UnsupportedFacetsSHA256) ||
		!reflect.DeepEqual(schemaUnsupportedFacets(base), schemaUnsupportedFacets(prop)) ||
		len(base.Properties) != len(prop.Properties) {
		return false
	}
	for name, baseChild := range base.Properties {
		propChild, ok := prop.Properties[name]
		if !ok || !schemaEquivalent(baseChild, propChild, baseSpec, propSpec, depth+1, seen) {
			return false
		}
	}
	if (base.Items == nil) != (prop.Items == nil) ||
		(base.Items != nil && !schemaEquivalent(base.Items, prop.Items, baseSpec, propSpec, depth+1, seen)) {
		return false
	}
	if !schemaAlternativesEqual(base.OneOf, prop.OneOf, baseSpec, propSpec, seen) ||
		!schemaAlternativesEqual(base.AnyOf, prop.AnyOf, baseSpec, propSpec, seen) {
		return false
	}
	return true
}

func cloneSchemaRefPairs(in map[schemaRefPair]bool) map[schemaRefPair]bool {
	copy := make(map[schemaRefPair]bool, len(in))
	for pair := range in {
		copy[pair] = true
	}
	return copy
}

func equalSchemaStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	aCopy, bCopy := append([]string(nil), a...), append([]string(nil), b...)
	sort.Strings(aCopy)
	sort.Strings(bCopy)
	for i := range aCopy {
		if aCopy[i] != bCopy[i] {
			return false
		}
	}
	return true
}

func schemaUnsupportedFacets(schema *Schema) map[string]any {
	if schema == nil || len(schema.Raw) == 0 {
		return nil
	}
	out := make(map[string]any)
	for key, value := range schema.Raw {
		switch key {
		case "$ref", "type", "nullable", "description", "properties", "required", "items", "oneOf", "anyOf":
			continue
		default:
			out[key] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// resolveRef follows a $ref chain against the parent Spec's
// Components. Cycles are broken at depth 8 (the OpenAPI
// ecosystem does not allow arbitrary nesting; deeper chains
// indicate a malformed spec). When the ref can't be resolved,
// the schema is returned unchanged — the loader's error path
// would have surfaced the malformed spec, so this is a
// defensive no-op.
func resolveRef(s *Schema, spec *Spec) *Schema {
	const maxDepth = 8
	for i := 0; i < maxDepth && s != nil && s.Ref != ""; i++ {
		name := strings.TrimPrefix(s.Ref, "#/components/schemas/")
		if spec == nil {
			return s
		}
		target, ok := spec.Components[name]
		if !ok {
			return s
		}
		s = target
	}
	return s
}

// joinPath prepends a parent property path to a child path
// while preserving the dot-separated shape. Both inputs may
// be empty; the result collapses cleanly.
func joinPath(parent, child string) string {
	switch {
	case parent == "":
		return child
	case child == "":
		return parent
	default:
		return parent + "." + child
	}
}

// unionSortedKeys returns the sorted union of keys across the
// given string-keyed maps. nil maps are treated as empty.
func unionSortedKeys[V any](maps ...map[string]V) []string {
	seen := map[string]struct{}{}
	for _, m := range maps {
		for k := range m {
			seen[k] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// keySetString returns a set (map) view of a string slice. nil
// and empty inputs both yield an empty (non-nil) map.
func keySetString(s []string) map[string]struct{} {
	out := make(map[string]struct{}, len(s))
	for _, v := range s {
		out[v] = struct{}{}
	}
	return out
}
