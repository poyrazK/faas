// Package openapidiff is the structural OpenAPI 3.1 differ that
// powers the schema-break signal in `gregale deploy --diff` (PR-2
// of the deploy-diff cluster; PR-0 / PR-1 in main since #860 / #869).
//
// The package is daemon-neutral: no imports of pkg/api, apid, or
// schedd. It exposes a single [Spec] type — a normalised view of
// the embedded `pkg/apid/openapi.yaml` — plus a [Compare] function
// that walks two Specs and emits a slice of [SchemaBreak] rows.
//
// Why a hand-rolled walker rather than kin-openapi:
//
//   - The repo pins gopkg.in/yaml.v3 for the embedded OpenAPI handler
//     (see pkg/apid/openapi_handler.go) — adding a second dependency
//     stack just for a structural diff is overkill for v1.
//   - The structural surface PR-2 cares about is narrow: Paths →
//     Operations → Responses → Content → Schema, with property
//     order / description whitespace / [T, 'null'] ≡ nullable
//     noise stripped. A focused walker makes the noise rules
//     explicit and the differ trivially testable without spinning
//     up a $ref-resolver.
//   - OpenAPI 3.1 features outside this narrow surface (allOf
//     composition, link objects, webhooks) are deliberately out of
//     scope. Changes to unsupported response-schema facets are surfaced
//     as unknown rather than being misclassified as compatible.
//
// The loader reads the embedded `pkg/apid/openapi.yaml` directly via
// the existing [apid.OpenAPIYAML] seam — do NOT add a second
// //go:embed source. The embedded file is regenerated from
// api/openapi.yaml by `make spec-sync` per memory
// [spec-sync-stale-embed-on-openapi-change]; the loader therefore
// sees the exact spec the binary serves.
package openapidiff

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/onebox-faas/faas/pkg/apid"
	"gopkg.in/yaml.v3"
)

// SchemaKind is the categorisation we emit on a [SchemaBreak].
// Mirrors the noise-tolerant categories used for response contracts:
// a type change, a removed field, an added or removed required guarantee, or a
// nullability flip. Changed oneOf / anyOf schemas and unsupported
// response-schema facets are emitted as [SchemaUnknown] because their
// compatibility is not classified.
type SchemaKind string

const (
	// SchemaKindTypeChange fires when a schema's `type` changes
	// (string → object, integer → number, etc.). The most common
	// wire-shape break — clients that previously decoded as
	// `string` will now see a number.
	SchemaKindTypeChange SchemaKind = "type_change"
	// SchemaKindFieldRemoved fires when a property present on the
	// baseline schema is absent from the proposed schema. Clients
	// that read that field will get null/undefined.
	SchemaKindFieldRemoved SchemaKind = "field_removed"
	// SchemaKindRequiredAdded fires when a response property that was
	// optional on the baseline is guaranteed by the proposed contract.
	SchemaKindRequiredAdded SchemaKind = "required_added"
	// SchemaKindRequiredRemoved fires when a response property that was
	// guaranteed by the baseline becomes optional in the proposed contract.
	SchemaKindRequiredRemoved SchemaKind = "required_removed"
	// SchemaKindNullabilityChange fires when the nullable facet
	// flips: baseline schema was `nullable: true` (or `[T, 'null']`
	// per OpenAPI 3.1) but the proposed is not, or vice versa. Per
	// the noise rule the differ MUST treat `nullable: true` and
	// `[T, 'null']` as semantically equal, so this fires only on a
	// real flip.
	SchemaKindNullabilityChange SchemaKind = "nullability_change"
)

// SchemaUnknownCode identifies a response-schema difference the structural
// comparator cannot classify safely.
type SchemaUnknownCode string

const (
	// SchemaUnknownUnsupportedUnionChange means a OneOf / AnyOf union changed.
	// The comparator reports the affected contract location without claiming
	// that the change is breaking or additive.
	SchemaUnknownUnsupportedUnionChange SchemaUnknownCode = "unsupported_union_change"
	// SchemaUnknownUnionBaselineIncomplete means a snapshot predates opaque
	// facet fingerprints, so a union's raw schema changes cannot be excluded.
	SchemaUnknownUnionBaselineIncomplete SchemaUnknownCode = "union_baseline_incomplete"
	// SchemaUnknownUnsupportedSchemaChange means a response schema changed in
	// a facet the structural comparator does not classify (for example enum,
	// format, numeric constraints, or composition keywords).
	SchemaUnknownUnsupportedSchemaChange SchemaUnknownCode = "unsupported_schema_change"
	// SchemaUnknownSchemaBaselineIncomplete means a snapshot predates opaque
	// facet fingerprints, so the comparator cannot establish that unsupported
	// facets were absent or unchanged.
	SchemaUnknownSchemaBaselineIncomplete SchemaUnknownCode = "schema_baseline_incomplete"
)

// emptyUnsupportedFacetsSHA256 is the SHA-256 of JSON null, the canonical
// encoding produced for a schema with no unsupported facets.
const emptyUnsupportedFacetsSHA256 = "74234e98afe7498fb5daf1f36ac2d78acc339464f950703b8c019892f982b90b"

// SchemaUnknown is one response-schema finding that the supported checks
// cannot classify. Promotion checks must treat unknowns as blocking while
// keeping them separate from confirmed SchemaBreak rows.
type SchemaUnknown struct {
	Path         string
	Method       string
	Status       string
	PathInSchema string
	Code         SchemaUnknownCode
}

// SchemaComparison is the detailed result from [CompareDetailed]. Breaks
// are confirmed contract regressions; Unknowns are unsupported schema
// changes or incomplete baselines whose compatibility cannot be established.
type SchemaComparison struct {
	Breaks   []SchemaBreak
	Unknowns []SchemaUnknown
}

// SchemaBreak is one row emitted by [Compare]. The wire form
// piggybacks on [pkg/deploydiff.Break] — the engine converts each
// SchemaBreak into one Break with Code "schema_response_changed"
// and Field set to SchemaBreak.Path, so the customer sees the
// exact endpoint + response shape that would change.
//
// Field naming matches the handoff: Path = "/v1/apps/{slug}",
// Method = "get", Status = "200", Kind = the categorical break.
// PathInSchema is a dotted path into the schema body for
// SchemaKinds that target a property (e.g. "properties.email").
// It is empty for SchemaKindTypeChange on the top-level schema.
type SchemaBreak struct {
	// Path is the OpenAPI path key, verbatim (e.g. "/v1/apps/{slug}").
	// Tied to the path key the embedded spec declares.
	Path string
	// Method is the HTTP method lower-case ("get", "post", …).
	Method string
	// Status is the response status as a string ("200", "404", …).
	// "default" is treated like any other status.
	Status string
	// Kind is the categorical break.
	Kind SchemaKind
	// PathInSchema is a dotted property path into the schema body
	// ("" for top-level, "properties.email" for the email property,
	// "properties.email.properties.length" for a nested one). Empty
	// for SchemaKindTypeChange targeting the top-level schema.
	PathInSchema string
	// Before is the baseline value the kind is anchored to. For
	// SchemaKindFieldRemoved it is the field name; for
	// SchemaKindTypeChange it is the baseline type string; for
	// SchemaKindRequiredAdded it is the new required field name; for
	// SchemaKindRequiredRemoved it is the field no longer guaranteed.
	// Exposed as `any` so callers can render as they wish — the
	// engine wraps it in anyJSON for the wire.
	Before any
	// After is the proposed value (mirror of Before semantics).
	After any
}

// Spec is the normalised OpenAPI 3.1 view. Two Specs feed into
// [Compare]: one for the baseline, one for the proposed. Both are
// produced by [Load] (or [LoadBytes] for tests).
//
// The shape is intentionally narrow: Paths → Methods → Statuses →
// ContentTypes → normalisedSchema. The Components map is held
// alongside for $ref resolution by the differ.
type Spec struct {
	// Raw retains the immutable document for bounded local request references
	// and root security metadata. It is never serialized in comparison results.
	Raw map[string]any `json:"-"`
	// Paths is keyed by the path string from the embedded spec.
	Paths map[string]*PathItem
	// Components is the resolved-by-name schema map. Populated by
	// Load(); the differ dereferences $ref against it. Tests that
	// construct Specs by hand can leave this nil and avoid $ref
	// entirely.
	Components map[string]*Schema
	// version is "3.1" or "3.0" — recorded for noise-rule decisions
	// (the [T, 'null'] rule only applies to 3.1). Exposed via
	// [Spec.OpenAPIVersion].
	version string
}

// OpenAPIVersion returns the OpenAPI version string the spec
// declared ("3.1" or "3.0"). Empty when the loader could not
// determine it (which is itself a build-time invariant violation).
func (s *Spec) OpenAPIVersion() string { return s.version }

// PathItem is one OpenAPI path. Each method map entry holds the
// [Operation] for that verb. Methods are lower-case to match the
// differ's path traversal.
type PathItem struct {
	// Raw preserves path-level OpenAPI metadata that the structural differ does
	// not interpret, including shared parameters and servers. Auto rendering
	// overlays the normalized methods onto this map.
	Raw map[string]any `json:"-"`
	// Methods is keyed by lower-case HTTP method (get, post, …).
	Methods map[string]*Operation
	// Parameters is the shared parameter list (path-level
	// parameters). Held so the differ can walk them; PR-2's break
	// signal ignores parameter changes (those are config, not
	// schema), but the field is captured for completeness.
	Parameters []*Schema
}

// Operation is one verb of a path. Responses is keyed by the
// status string ("200", "404", "default", …). Content is keyed
// by content type ("application/json", …).
type Operation struct {
	// Raw preserves operation metadata outside the structural response model,
	// such as operationId, parameters, requestBody, security, tags and servers.
	Raw map[string]any `json:"-"`
	// Responses is keyed by status string. The differ walks each
	// response's content schemas.
	Responses map[string]*Response
}

// Response is one status response. Content holds the per-content-type
// schema payload.
type Response struct {
	// Raw preserves response descriptions, headers, links and extensions.
	Raw map[string]any `json:"-"`
	// Content is keyed by MIME type. Empty when the response has
	// no body (e.g. 204 No Content).
	Content map[string]*Schema
}

// Schema is a normalised JSON Schema view. The differ walks
// recursively through Properties and Items. Changed OneOf / AnyOf unions and
// other unsupported schema facets are reported as unknown.
//
// Required is the unsorted set declared on the schema. The
// loader sorts it on read so the differ sees stable ordering.
//
// Nullable is the OpenAPI 3.1 [T, 'null'] form collapsed to a
// bool per the noise rule. For OpenAPI 3.0 docs the loader
// honours the `nullable: true` facet directly.
//
// $ref is preserved verbatim so the differ can resolve it against
// the parent Spec's Components map. The loader does NOT inline
// refs — that's the differ's job, and only at the points it needs
// to walk into the schema body.
type Schema struct {
	// Raw preserves JSON Schema facets outside the focused structural differ
	// (format, enum, constraints, allOf, additionalProperties, and extensions).
	Raw map[string]any `json:"-"`
	// UnsupportedFacetsSHA256 preserves an opaque, non-reversible marker in
	// deployment snapshots so raw-only response-schema changes remain
	// detectable without storing examples or other raw facet values.
	UnsupportedFacetsSHA256 string `json:"-"`
	// Type is the JSON Schema type string ("object", "string",
	// "integer", "number", "boolean", "array", "null"). Empty
	// for oneOf/anyOf unions.
	Type string `json:"type,omitempty"`
	// Properties is keyed by property name. Nil for non-object
	// schemas.
	Properties map[string]*Schema `json:"properties,omitempty"`
	// Required is the sorted required-property list.
	Required []string `json:"required,omitempty"`
	// Items is the array-element schema. Nil for non-array schemas.
	Items *Schema `json:"items,omitempty"`
	// Nullable is true when the schema accepts null. Captures
	// both OpenAPI 3.0 `nullable: true` and OpenAPI 3.1 `[T,
	// 'null']` form per the noise rule.
	Nullable bool `json:"nullable,omitempty"`
	// OneOf / AnyOf hold union alternatives. Nil when not a
	// union. The differ keeps them opaque and emits an unknown
	// finding when their observable schema shape changes.
	OneOf []*Schema `json:"oneOf,omitempty"`
	AnyOf []*Schema `json:"anyOf,omitempty"`
	// Ref is the unresolved $ref string ("#/components/schemas/Foo").
	// Empty when inlined.
	Ref string `json:"$ref,omitempty"`
	// Description is the human-readable description. Held for
	// completeness; the differ strips whitespace before any
	// comparison so description-only changes never fire a break.
	Description string `json:"description,omitempty"`
}

// Load reads the embedded pkg/apid/openapi.yaml via the existing
// [apid.OpenAPIYAML] seam and returns a normalised [Spec]. Errors
// surface only when the embedded spec is malformed — a build-time
// invariant violation that [pkg/apid.spec_compliance_test.go] is
// designed to catch at PR time, so production deployments never
// reach this error path.
func Load() (*Spec, error) {
	return LoadBytes(apid.OpenAPIYAML())
}

// LoadBytes parses a raw OpenAPI 3.x YAML document and returns a
// normalised [Spec]. It is used for customer-supplied local documents
// and tests; callers inspecting Gregale's served contract should use
// [Load] so the embedded spec remains the source of truth.
func LoadBytes(data []byte) (*Spec, error) {
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("openapidiff: parse yaml: %w", err)
	}
	if len(doc) == 0 {
		return nil, errors.New("openapidiff: empty spec")
	}
	spec := &Spec{
		Raw:        cloneOpenAPIMap(doc),
		Paths:      map[string]*PathItem{},
		Components: map[string]*Schema{},
	}
	if v, ok := doc["openapi"].(string); ok {
		spec.version = v
	}
	// Top-level paths.
	if rawPaths, ok := doc["paths"].(map[string]any); ok {
		for pathKey, rawPI := range rawPaths {
			pi, err := parsePathItem(rawPI)
			if err != nil {
				return nil, fmt.Errorf("openapidiff: path %q: %w", pathKey, err)
			}
			spec.Paths[pathKey] = pi
		}
	}
	// Components.Schemas.
	if comps, ok := doc["components"].(map[string]any); ok {
		if schemas, ok := comps["schemas"].(map[string]any); ok {
			for name, raw := range schemas {
				sch, err := parseSchema(raw)
				if err != nil {
					return nil, fmt.Errorf("openapidiff: schema %q: %w", name, err)
				}
				spec.Components[name] = sch
			}
		}
	}
	return spec, nil
}

// parsePathItem converts the raw YAML map for one OpenAPI Path Item
// into a [PathItem]. The recognised method keys are the standard
// HTTP verbs (lower-case); anything else (e.g. "summary",
// "description") is ignored for the structural diff.
func parsePathItem(raw any) (*PathItem, error) {
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("expected map, got %T", raw)
	}
	pi := &PathItem{Raw: cloneOpenAPIMap(m), Methods: map[string]*Operation{}}
	for _, method := range []string{"get", "post", "put", "patch", "delete", "options", "head"} {
		if rawOp, ok := m[method].(map[string]any); ok {
			op, err := parseOperation(rawOp)
			if err != nil {
				return nil, fmt.Errorf("method %q: %w", method, err)
			}
			pi.Methods[method] = op
		}
	}
	return pi, nil
}

// parseOperation converts the raw YAML map for one verb into an
// [Operation]. Only the `responses` block is structurally walked —
// parameters / requestBody / security are held on the [Operation]
// only via the [PathItem] parameter list, which PR-2 ignores for
// break detection.
func parseOperation(raw map[string]any) (*Operation, error) {
	op := &Operation{Raw: cloneOpenAPIMap(raw), Responses: map[string]*Response{}}
	if rawResp, ok := raw["responses"].(map[string]any); ok {
		for status, rawR := range rawResp {
			resp, err := parseResponse(rawR)
			if err != nil {
				return nil, fmt.Errorf("status %q: %w", status, err)
			}
			op.Responses[status] = resp
		}
	}
	return op, nil
}

// parseResponse converts the raw YAML map for one response status
// into a [Response]. $ref responses ({"$ref":
// "#/components/responses/Unauthorized"}) are captured as a
// Response with empty Content; the differ does not chase
// response $refs in PR-2 (kept for completeness).
func parseResponse(raw any) (*Response, error) {
	resp := &Response{Content: map[string]*Schema{}}
	m, ok := raw.(map[string]any)
	if !ok {
		return resp, nil
	}
	resp.Raw = cloneOpenAPIMap(m)
	if rawC, ok := m["content"].(map[string]any); ok {
		for ct, rawS := range rawC {
			ctm, ok := rawS.(map[string]any)
			if !ok {
				continue
			}
			if rawSch, ok := ctm["schema"].(map[string]any); ok {
				sch, err := parseSchema(rawSch)
				if err != nil {
					return nil, fmt.Errorf("content-type %q: %w", ct, err)
				}
				resp.Content[ct] = sch
			}
		}
	}
	return resp, nil
}

// parseSchema converts a raw YAML map (the OpenAPI "schema:" value
// or any nested node) into a normalised [Schema]. The noise rules
// are applied here:
//
//  1. [T, 'null'] on OpenAPI 3.1 → Type = T's type, Nullable = true.
//     The OpenAPI 3.1 form is a JSON Schema 2020-12 two-element
//     array; `nullable: true` is the 3.0 form. Both are equivalent
//     per memory [pr-819-openapi-nullable-3-1].
//
//  2. properties map keys are sorted at every level so the differ
//     sees stable ordering.
//
//  3. description whitespace is trimmed (the differ strips
//     fully so description-only changes never fire a break).
func parseSchema(raw any) (*Schema, error) {
	m, ok := raw.(map[string]any)
	if !ok {
		return &Schema{}, nil
	}
	sch := &Schema{Raw: cloneOpenAPIMap(m)}
	opaque, err := json.Marshal(schemaUnsupportedFacets(sch))
	if err != nil {
		return nil, fmt.Errorf("unsupported facets: %w", err)
	}
	opaqueHash := sha256.Sum256(opaque)
	sch.UnsupportedFacetsSHA256 = hex.EncodeToString(opaqueHash[:])
	// $ref short-circuits — the differ resolves it later.
	if ref, ok := m["$ref"].(string); ok {
		sch.Ref = ref
		return sch, nil
	}
	// OpenAPI 3.1 nullable form: type is a 2-element array with
	// one of "null".
	if tArr, ok := m["type"].([]any); ok && len(tArr) == 2 {
		var nonNull string
		var sawNull bool
		for _, v := range tArr {
			s, ok := v.(string)
			if !ok {
				continue
			}
			if s == "null" {
				sawNull = true
				continue
			}
			nonNull = s
		}
		if sawNull && nonNull != "" {
			sch.Type = nonNull
			sch.Nullable = true
		}
	} else if t, ok := m["type"].(string); ok {
		sch.Type = t
	}
	// OpenAPI 3.0 nullable: true facet.
	if n, ok := m["nullable"].(bool); ok && n {
		sch.Nullable = true
	}
	if d, ok := m["description"].(string); ok {
		sch.Description = trimWS(d)
	}
	if props, ok := m["properties"].(map[string]any); ok {
		sch.Properties = make(map[string]*Schema, len(props))
		// Sort property names for stable walker traversal.
		names := make([]string, 0, len(props))
		for n := range props {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, name := range names {
			child, err := parseSchema(props[name])
			if err != nil {
				return nil, fmt.Errorf("property %q: %w", name, err)
			}
			sch.Properties[name] = child
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
	if rawItems, ok := m["items"].(map[string]any); ok {
		child, err := parseSchema(rawItems)
		if err != nil {
			return nil, fmt.Errorf("items: %w", err)
		}
		sch.Items = child
	}
	if oneOf, ok := m["oneOf"].([]any); ok {
		for _, v := range oneOf {
			child, err := parseSchema(v)
			if err != nil {
				return nil, fmt.Errorf("oneOf: %w", err)
			}
			sch.OneOf = append(sch.OneOf, child)
		}
	}
	if anyOf, ok := m["anyOf"].([]any); ok {
		for _, v := range anyOf {
			child, err := parseSchema(v)
			if err != nil {
				return nil, fmt.Errorf("anyOf: %w", err)
			}
			sch.AnyOf = append(sch.AnyOf, child)
		}
	}
	return sch, nil
}

// cloneOpenAPIMap copies the current object level. Nested maps are treated as
// immutable loader input; renderers replace only the normalized top-level
// fields they own, so a shallow copy avoids aliasing without a JSON round trip.
func cloneOpenAPIMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

// trimWS collapses runs of whitespace to a single space and strips
// leading/trailing whitespace. Description strings in OpenAPI YAML
// are commonly multi-line block scalars with arbitrary indentation;
// normalising them here means the differ never has to think about
// whitespace when comparing descriptions.
func trimWS(s string) string {
	var b bytes.Buffer
	inWS := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			if !inWS {
				b.WriteRune(' ')
				inWS = true
			}
			continue
		}
		inWS = false
		b.WriteRune(r)
	}
	out := b.String()
	// Trim leading / trailing spaces.
	for len(out) > 0 && out[0] == ' ' {
		out = out[1:]
	}
	for len(out) > 0 && out[len(out)-1] == ' ' {
		out = out[:len(out)-1]
	}
	return out
}
