package openapidiff

import (
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
//  3. Required — added. Removing a required field is NOT a break
//     (clients sending it are tolerant). Adding a required field
//     IS a break (existing clients omit it, the server 400s).
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
// OneOf / AnyOf unions are treated as opaque (any non-nil union
// is a black box). PR-2 deliberately does not walk into them —
// the customer-visible break surface is the four kinds above.
func Compare(baseline, proposed *Spec) []SchemaBreak {
	if baseline == nil || proposed == nil {
		return nil
	}
	var breaks []SchemaBreak
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
				breaks = append(breaks, SchemaBreak{
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
				breaks = append(breaks, SchemaBreak{
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
					breaks = append(breaks, SchemaBreak{
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
						breaks = append(breaks, SchemaBreak{
							Path: pathKey, Method: method, Status: status,
							Kind:         SchemaKindFieldRemoved,
							PathInSchema: ct,
							Before:       ct,
						})
						continue
					}
					breaks = append(breaks, diffSchema(pathKey, method, status, ct, baseSch, propSch, baseline, proposed)...)
				}
			}
		}
	}
	return breaks
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
func diffSchema(path, method, status, ct string, base, prop *Schema, baseSpec, propSpec *Spec) []SchemaBreak {
	_ = ct // ct is already encoded in the breaks; kept in the signature for readability.
	var breaks []SchemaBreak
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
			breaks = append(breaks, SchemaBreak{
				Path: path, Method: method, Status: status,
				Kind:   SchemaKindTypeChange,
				Before: base.Type, After: prop.Type,
			})
		}
	}
	// 2. Nullability change.
	if base.Nullable != prop.Nullable {
		breaks = append(breaks, SchemaBreak{
			Path: path, Method: method, Status: status,
			Kind:         SchemaKindNullabilityChange,
			PathInSchema: "",
			Before:       base.Nullable, After: prop.Nullable,
		})
	}
	// 3. Removed properties.
	for name := range base.Properties {
		if _, ok := prop.Properties[name]; !ok {
			breaks = append(breaks, SchemaBreak{
				Path: path, Method: method, Status: status,
				Kind:         SchemaKindFieldRemoved,
				PathInSchema: "properties." + name,
				Before:       name,
			})
		}
	}
	// 4. Required added.
	baseReq := keySetString(base.Required)
	propReq := keySetString(prop.Required)
	for _, name := range prop.Required {
		if _, ok := baseReq[name]; !ok {
			breaks = append(breaks, SchemaBreak{
				Path: path, Method: method, Status: status,
				Kind:         SchemaKindRequiredAdded,
				PathInSchema: "properties." + name,
				After:        name,
			})
		}
	}
	// 5. Recurse into shared properties + Items.
	for name, baseChild := range base.Properties {
		if propChild, ok := prop.Properties[name]; ok {
			childPathInSchema := "properties." + name
			for _, b := range diffSchema(path, method, status, ct, baseChild, propChild, baseSpec, propSpec) {
				b.PathInSchema = joinPath(b.PathInSchema, childPathInSchema)
				breaks = append(breaks, b)
			}
		}
	}
	if base.Items != nil && prop.Items != nil {
		for _, b := range diffSchema(path, method, status, ct, base.Items, prop.Items, baseSpec, propSpec) {
			b.PathInSchema = joinPath(b.PathInSchema, "items")
			breaks = append(breaks, b)
		}
	}
	// Avoid an "unused variable" warning when propReq is
	// only consulted in the loop above.
	_ = propReq
	return breaks
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
