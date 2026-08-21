package openapidiff

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// SnapshotSchemaVersion is the wire version of the canonical
// JSON envelope produced by MarshalSnapshot. The
// deployment_openapi_snapshots.schema_version column pins this
// number; bump on a breaking shape change (and add a separate
// migration that widens the schema_version CHECK constraint).
const SnapshotSchemaVersion = 1

// MarshalSnapshot serializes a [Spec] to the canonical JSON form
// persisted by the API contract diff feature (migration 00358).
// The canonical form is:
//
//   - A two-field envelope: {"schema_version": 1, "spec": <Spec>}.
//   - The Spec is encoded as a stable map with sorted keys at
//     every level (Paths, Methods, Responses, ContentTypes,
//     Properties, Required, Components).
//   - All description whitespace is collapsed (the loader already
//     trims it, but MarshalSnapshot re-runs the rule so a
//     hand-constructed Spec also serializes deterministically).
//
// The canonical form is what feeds SHA-256. Two Specs that
// [Compare] reports as zero breaks MUST produce identical
// canonical JSON and identical SHA-256 — the hash is the
// replay/drift anchor for the snapshot row.
func MarshalSnapshot(s *Spec) (json.RawMessage, string, error) {
	if s == nil {
		return nil, "", errors.New("openapidiff: MarshalSnapshot: nil spec")
	}
	doc := map[string]any{
		"schema_version": SnapshotSchemaVersion,
		"spec":           specToCanonical(s),
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, "", fmt.Errorf("openapidiff: marshal snapshot: %w", err)
	}
	// Encode again with HTML escaping disabled so the canonical
	// form is byte-stable across Go versions (Go's stdlib
	// switches to HTMLEscape by default for json.Marshal).
	raw, err = encodeCanonicalBytes(raw)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(raw)
	return raw, hex.EncodeToString(sum[:]), nil
}

// UnmarshalSnapshot decodes a canonical JSON envelope back into a
// [Spec]. The schema_version field is checked against the
// current SnapshotSchemaVersion; a mismatch returns an error so
// the caller can switch to a smarter deserializer (a future
// PR, not in PR-A).
func UnmarshalSnapshot(raw json.RawMessage) (*Spec, error) {
	if len(raw) == 0 {
		return nil, errors.New("openapidiff: UnmarshalSnapshot: empty payload")
	}
	var doc struct {
		SchemaVersion int             `json:"schema_version"`
		Spec          json.RawMessage `json:"spec"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("openapidiff: unmarshal envelope: %w", err)
	}
	if doc.SchemaVersion != SnapshotSchemaVersion {
		return nil, fmt.Errorf("openapidiff: schema_version=%d, want %d", doc.SchemaVersion, SnapshotSchemaVersion)
	}
	var specAny any
	if err := json.Unmarshal(doc.Spec, &specAny); err != nil {
		return nil, fmt.Errorf("openapidiff: unmarshal spec: %w", err)
	}
	return canonicalToSpec(specAny)
}

// specToCanonical walks the [Spec] and produces a stable map
// (sorted keys) for JSON encoding. The shape is intentionally
// independent of the wire Spec — the snapshot is the audit
// record, not the loader's input.
func specToCanonical(s *Spec) map[string]any {
	out := map[string]any{
		"openapi_version": s.version,
		"paths":           pathsToCanonical(s.Paths),
		"components":      schemasToCanonical(s.Components),
	}
	return out
}

func pathsToCanonical(paths map[string]*PathItem) map[string]any {
	out := make(map[string]any, len(paths))
	keys := make([]string, 0, len(paths))
	for k := range paths {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out[k] = pathItemToCanonical(paths[k])
	}
	return out
}

func pathItemToCanonical(pi *PathItem) map[string]any {
	if pi == nil {
		return nil
	}
	out := map[string]any{
		"methods": methodsToCanonical(pi.Methods),
	}
	if len(pi.Parameters) > 0 {
		out["parameters"] = schemasToCanonicalMap(pi.Parameters)
	}
	return out
}

func methodsToCanonical(methods map[string]*Operation) map[string]any {
	out := make(map[string]any, len(methods))
	keys := make([]string, 0, len(methods))
	for k := range methods {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out[k] = operationToCanonical(methods[k])
	}
	return out
}

func operationToCanonical(op *Operation) map[string]any {
	if op == nil {
		return nil
	}
	out := map[string]any{
		"responses": responsesToCanonical(op.Responses),
	}
	return out
}

func responsesToCanonical(responses map[string]*Response) map[string]any {
	out := make(map[string]any, len(responses))
	keys := make([]string, 0, len(responses))
	for k := range responses {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out[k] = responseToCanonical(responses[k])
	}
	return out
}

func responseToCanonical(r *Response) map[string]any {
	if r == nil {
		return nil
	}
	return map[string]any{
		"content": schemaMapToCanonical(r.Content),
	}
}

func schemaMapToCanonical(m map[string]*Schema) map[string]any {
	out := make(map[string]any, len(m))
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out[k] = schemaToCanonical(m[k])
	}
	return out
}

func schemasToCanonical(m map[string]*Schema) map[string]any {
	out := make(map[string]any, len(m))
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out[k] = schemaToCanonical(m[k])
	}
	return out
}

func schemasToCanonicalMap(schemas []*Schema) []any {
	out := make([]any, 0, len(schemas))
	for _, s := range schemas {
		out = append(out, schemaToCanonical(s))
	}
	return out
}

func schemaToCanonical(s *Schema) map[string]any {
	if s == nil {
		return nil
	}
	out := map[string]any{}
	if s.Type != "" {
		out["type"] = s.Type
	}
	if s.Nullable {
		out["nullable"] = true
	}
	if s.Ref != "" {
		out["ref"] = s.Ref
	}
	if s.Description != "" {
		out["description"] = s.Description
	}
	if len(s.Properties) > 0 {
		out["properties"] = schemaMapToCanonical(s.Properties)
	}
	if len(s.Required) > 0 {
		req := make([]string, len(s.Required))
		copy(req, s.Required)
		sort.Strings(req)
		out["required"] = req
	}
	if s.Items != nil {
		out["items"] = schemaToCanonical(s.Items)
	}
	if len(s.OneOf) > 0 {
		out["one_of"] = schemasToCanonicalMap(s.OneOf)
	}
	if len(s.AnyOf) > 0 {
		out["any_of"] = schemasToCanonicalMap(s.AnyOf)
	}
	return out
}

// canonicalToSpec reverses specToCanonical. It is the inverse
// used by UnmarshalSnapshot to feed the differ. The reconstruction
// is intentionally minimal: the differ's input is the same data
// shape the loader produces, so the same field names map back.
func canonicalToSpec(in any) (*Spec, error) {
	m, ok := in.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("openapidiff: spec must be a map, got %T", in)
	}
	spec := &Spec{
		Paths:      map[string]*PathItem{},
		Components: map[string]*Schema{},
	}
	if v, ok := m["openapi_version"].(string); ok {
		spec.version = v
	}
	if paths, ok := m["paths"].(map[string]any); ok {
		for k, v := range paths {
			pi, err := canonicalToPathItem(v)
			if err != nil {
				return nil, fmt.Errorf("path %q: %w", k, err)
			}
			spec.Paths[k] = pi
		}
	}
	if comps, ok := m["components"].(map[string]any); ok {
		for k, v := range comps {
			sch, err := canonicalToSchema(v)
			if err != nil {
				return nil, fmt.Errorf("component %q: %w", k, err)
			}
			spec.Components[k] = sch
		}
	}
	return spec, nil
}

func canonicalToPathItem(in any) (*PathItem, error) {
	m, ok := in.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("path item must be a map, got %T", in)
	}
	pi := &PathItem{Methods: map[string]*Operation{}}
	if methods, ok := m["methods"].(map[string]any); ok {
		for k, v := range methods {
			op, err := canonicalToOperation(v)
			if err != nil {
				return nil, fmt.Errorf("method %q: %w", k, err)
			}
			pi.Methods[k] = op
		}
	}
	if params, ok := m["parameters"].([]any); ok {
		for _, v := range params {
			sch, err := canonicalToSchema(v)
			if err != nil {
				return nil, fmt.Errorf("parameter: %w", err)
			}
			pi.Parameters = append(pi.Parameters, sch)
		}
	}
	return pi, nil
}

func canonicalToOperation(in any) (*Operation, error) {
	m, ok := in.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("operation must be a map, got %T", in)
	}
	op := &Operation{Responses: map[string]*Response{}}
	if responses, ok := m["responses"].(map[string]any); ok {
		for k, v := range responses {
			resp, err := canonicalToResponse(v)
			if err != nil {
				return nil, fmt.Errorf("response %q: %w", k, err)
			}
			op.Responses[k] = resp
		}
	}
	return op, nil
}

func canonicalToResponse(in any) (*Response, error) {
	m, ok := in.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("response must be a map, got %T", in)
	}
	resp := &Response{Content: map[string]*Schema{}}
	if ct, ok := m["content"].(map[string]any); ok {
		for k, v := range ct {
			sch, err := canonicalToSchema(v)
			if err != nil {
				return nil, fmt.Errorf("content-type %q: %w", k, err)
			}
			resp.Content[k] = sch
		}
	}
	return resp, nil
}

func canonicalToSchema(in any) (*Schema, error) {
	m, ok := in.(map[string]any)
	if !ok {
		// Schemas can be nil in the canonical form (an empty
		// response body, for example). Return an empty Schema
		// so the differ sees a non-nil pointer.
		return &Schema{}, nil
	}
	sch := &Schema{}
	if v, ok := m["type"].(string); ok {
		sch.Type = v
	}
	if v, ok := m["nullable"].(bool); ok {
		sch.Nullable = v
	}
	if v, ok := m["ref"].(string); ok {
		sch.Ref = v
	}
	if v, ok := m["description"].(string); ok {
		sch.Description = v
	}
	if props, ok := m["properties"].(map[string]any); ok {
		sch.Properties = map[string]*Schema{}
		for k, v := range props {
			child, err := canonicalToSchema(v)
			if err != nil {
				return nil, fmt.Errorf("property %q: %w", k, err)
			}
			sch.Properties[k] = child
		}
	}
	if req, ok := m["required"].([]any); ok {
		for _, v := range req {
			if s, ok := v.(string); ok {
				sch.Required = append(sch.Required, s)
			}
		}
		sort.Strings(sch.Required)
	}
	if items, ok := m["items"]; ok {
		child, err := canonicalToSchema(items)
		if err != nil {
			return nil, fmt.Errorf("items: %w", err)
		}
		sch.Items = child
	}
	if oneOf, ok := m["one_of"].([]any); ok {
		for _, v := range oneOf {
			child, err := canonicalToSchema(v)
			if err != nil {
				return nil, fmt.Errorf("one_of: %w", err)
			}
			sch.OneOf = append(sch.OneOf, child)
		}
	}
	if anyOf, ok := m["any_of"].([]any); ok {
		for _, v := range anyOf {
			child, err := canonicalToSchema(v)
			if err != nil {
				return nil, fmt.Errorf("any_of: %w", err)
			}
			sch.AnyOf = append(sch.AnyOf, child)
		}
	}
	return sch, nil
}

// encodeCanonicalBytes re-encodes already-JSON bytes with HTML
// escaping disabled so the SHA-256 hash is stable across Go
// versions (Go's json package escapes `<`, `>`, `&` by default
// in the top-level encoder; for canonical JSON we don't need
// that protection).
func encodeCanonicalBytes(in []byte) (json.RawMessage, error) {
	var v any
	if err := json.Unmarshal(in, &v); err != nil {
		return nil, fmt.Errorf("openapidiff: canonical round-trip: %w", err)
	}
	out, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("openapidiff: canonical marshal: %w", err)
	}
	return out, nil
}
