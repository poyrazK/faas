package openapidiff

import (
	"sort"
	"strings"
)

// AdditiveChangeKind is the categorical flavour of an additive
// (compatible) change. The PR-C gate lists these with severity
// "warn" so the customer sees a "yes, this is fine" annotation,
// not a blocker.
type AdditiveChangeKind string

const (
	// AdditivePathAdded — a brand-new endpoint exists in the
	// proposed spec that was not in the baseline. New endpoints
	// are non-breaking: clients discover them via the response
	// headers / SDK regen.
	AdditivePathAdded AdditiveChangeKind = "path_added"
	// AdditiveMethodAdded — a new verb on an existing path.
	// Non-breaking — the existing client base still uses the
	// previously-supported verbs.
	AdditiveMethodAdded AdditiveChangeKind = "method_added"
	// AdditiveStatusAdded — a new response status on an
	// existing path/method. Non-breaking; existing clients
	// either ignore the new status or pre-emptively handle
	// it.
	AdditiveStatusAdded AdditiveChangeKind = "status_added"
	// AdditiveContentTypeAdded — a new Content-Type on an
	// existing response. Non-breaking — existing clients
	// continue to negotiate the previously-supported content
	// type.
	AdditiveContentTypeAdded AdditiveChangeKind = "content_type_added"
	// AdditivePropertyAdded — a new property on an existing
	// schema. Non-breaking: JSON Schema tolerates unknown
	// properties by default.
	AdditivePropertyAdded AdditiveChangeKind = "property_added"
	// AdditiveOptionalMadeRequired — a property that was
	// optional in the baseline is now optional in the proposed
	// (no change). NOT a break. Listed here for completeness
	// so the renderer can show "no change" if it wishes.
	AdditiveOptionalMadeRequired AdditiveChangeKind = "optional_to_required"
)

// AdditiveChange is the inverse of [SchemaBreak] — a
// baseline → proposed delta that is intentionally NOT a break.
// The PR-C engine emits these as [deploydiff.Change] rows with
// `kind: "add"` already on the wire so the customer sees them
// in the same UI as the diff engine's existing change rows.
//
// Anchor fields (Path, Method, Status, PathInSchema) follow the
// same shape as [SchemaBreak] so the renderer can render both
// break rows and additive rows side-by-side.
type AdditiveChange struct {
	Kind         AdditiveChangeKind
	Path         string
	Method       string
	Status       string
	PathInSchema string
	// Field is the renderer-friendly label. For path-added the
	// label is "endpoint GET /users/{id}"; for property-added
	// it is "properties.email".
	Field string
}

// CompareAdditive walks two Specs and emits one [AdditiveChange]
// per observed additive (compatible) delta. The walk mirrors
// [Compare] but reports the inverse cases: a path in proposed
// but not in baseline is a path-added; a method in proposed but
// not in baseline is a method-added; etc.
//
// The function is symmetric to [Compare]: identical inputs
// produce zero additive changes; every additive is a
// "baseline → proposed" delta the engine surfaces as a
// customer-facing message.
func CompareAdditive(baseline, proposed *Spec) []AdditiveChange {
	if baseline == nil || proposed == nil {
		return nil
	}
	var out []AdditiveChange
	// Path level.
	basePathKeys := unionSortedKeys(baseline.Paths)
	propPathKeys := unionSortedKeys(proposed.Paths)
	for _, p := range difference(propPathKeys, basePathKeys) {
		out = append(out, AdditiveChange{
			Kind:   AdditivePathAdded,
			Path:   p,
			Field:  "endpoint " + p,
			Method: "",
			Status: "",
		})
	}
	// Path × Method level.
	intersectPaths := intersection(basePathKeys, propPathKeys)
	for _, p := range intersectPaths {
		baseOp := baseline.Paths[p]
		propOp := proposed.Paths[p]
		if baseOp == nil || propOp == nil {
			continue
		}
		baseMethods := unionSortedKeys(baseOp.Methods)
		propMethods := unionSortedKeys(propOp.Methods)
		for _, m := range difference(propMethods, baseMethods) {
			out = append(out, AdditiveChange{
				Kind:   AdditiveMethodAdded,
				Path:   p,
				Method: m,
				Field:  "endpoint " + strings.ToUpper(m) + " " + p,
				Status: "",
			})
		}
		// Method × Status level.
		intersectMethods := intersection(baseMethods, propMethods)
		for _, m := range intersectMethods {
			bOp := baseOp.Methods[m]
			pOp := propOp.Methods[m]
			if bOp == nil || pOp == nil {
				continue
			}
			baseStatuses := unionSortedKeys(bOp.Responses)
			propStatuses := unionSortedKeys(pOp.Responses)
			for _, s := range difference(propStatuses, baseStatuses) {
				out = append(out, AdditiveChange{
					Kind:   AdditiveStatusAdded,
					Path:   p,
					Method: m,
					Status: s,
					Field:  "response " + strings.ToUpper(m) + " " + p + " " + s,
				})
			}
			// Status × Content-Type level.
			intersectStatuses := intersection(baseStatuses, propStatuses)
			for _, s := range intersectStatuses {
				bResp := bOp.Responses[s]
				pResp := pOp.Responses[s]
				if bResp == nil || pResp == nil {
					continue
				}
				baseCTs := unionSortedKeys(bResp.Content)
				propCTs := unionSortedKeys(pResp.Content)
				for _, ct := range difference(propCTs, baseCTs) {
					out = append(out, AdditiveChange{
						Kind:   AdditiveContentTypeAdded,
						Path:   p,
						Method: m,
						Status: s,
						Field:  "content-type " + ct + " " + strings.ToUpper(m) + " " + p + " " + s,
					})
				}
				// Content-Type × Properties level.
				intersectCTs := intersection(baseCTs, propCTs)
				for _, ct := range intersectCTs {
					bSch := bResp.Content[ct]
					pSch := pResp.Content[ct]
					if bSch == nil || pSch == nil {
						continue
					}
					out = appendAdditiveProperties(out, p, m, s, ct, "", bSch, pSch)
				}
			}
		}
	}
	// Sort to keep output deterministic.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		if out[i].Method != out[j].Method {
			return out[i].Method < out[j].Method
		}
		if out[i].Status != out[j].Status {
			return out[i].Status < out[j].Status
		}
		return out[i].PathInSchema < out[j].PathInSchema
	})
	return out
}

// appendAdditiveProperties walks the schema body and emits one
// AdditivePropertyAdded per property present in proposed but not
// in baseline. Recursion mirrors [Compare]'s traversal so the
// additive walks the same surface as the break signal.
func appendAdditiveProperties(out []AdditiveChange, path, method, status, ct, pathInSchema string, base, prop *Schema) []AdditiveChange {
	if prop == nil {
		return out
	}
	for name, pChild := range prop.Properties {
		childPath := joinPath(pathInSchema, "properties."+name)
		if _, ok := base.Properties[name]; !ok {
			out = append(out, AdditiveChange{
				Kind:         AdditivePropertyAdded,
				Path:         path,
				Method:       method,
				Status:       status,
				PathInSchema: childPath,
				Field:        childPath,
			})
		}
		// Recurse into shared children so a deeply-nested
		// property added fires a similarly-nested additive.
		if bChild, ok := base.Properties[name]; ok {
			out = appendAdditiveProperties(out, path, method, status, ct, childPath, bChild, pChild)
		}
	}
	if prop.Items != nil {
		itemsPath := joinPath(pathInSchema, "items")
		if base.Items == nil {
			out = append(out, AdditiveChange{
				Kind:         AdditivePropertyAdded,
				Path:         path,
				Method:       method,
				Status:       status,
				PathInSchema: itemsPath,
				Field:        itemsPath,
			})
		} else {
			out = appendAdditiveProperties(out, path, method, status, ct, itemsPath, base.Items, prop.Items)
		}
	}
	return out
}

// intersection returns sorted keys present in both a and b.
func intersection(a, b []string) []string {
	bSet := make(map[string]struct{}, len(b))
	for _, v := range b {
		bSet[v] = struct{}{}
	}
	var out []string
	for _, v := range a {
		if _, ok := bSet[v]; ok {
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

// difference returns sorted keys present in a but not in b.
func difference(a, b []string) []string {
	bSet := make(map[string]struct{}, len(b))
	for _, v := range b {
		bSet[v] = struct{}{}
	}
	var out []string
	for _, v := range a {
		if _, ok := bSet[v]; !ok {
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}
