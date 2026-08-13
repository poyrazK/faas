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
//   - OpenAPI 3.1 features outside this narrow surface (oneOf
//     discriminators, allOf composition, link objects, webhooks)
//     are deliberately out of scope for PR-2 and would be added
//     behind a feature flag if a customer demand surfaces.
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
	"errors"
	"fmt"
	"sort"

	"github.com/onebox-faas/faas/pkg/apid"
	"gopkg.in/yaml.v3"
)

// SchemaKind is the categorisation we emit on a [SchemaBreak].
// Mirrors the four noise-tolerant categories the handoff requires:
// a type change, a removed field, an added required, or a
// nullability flip. Anything more exotic (oneOf discriminator
// change, etc.) is intentionally out of scope for PR-2.
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
	// SchemaKindRequiredAdded fires when a property that was
	// optional on the baseline is required on the proposed. The
	// inverse (required → optional) is NOT a break — clients
	// sending extra fields are tolerant.
	SchemaKindRequiredAdded SchemaKind = "required_added"
	// SchemaKindNullabilityChange fires when the nullable facet
	// flips: baseline schema was `nullable: true` (or `[T, 'null']`
	// per OpenAPI 3.1) but the proposed is not, or vice versa. Per
	// the noise rule the differ MUST treat `nullable: true` and
	// `[T, 'null']` as semantically equal, so this fires only on a
	// real flip.
	SchemaKindNullabilityChange SchemaKind = "nullability_change"
)

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
	// SchemaKindRequiredAdded it is the new required field name.
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
	// Responses is keyed by status string. The differ walks each
	// response's content schemas.
	Responses map[string]*Response
}

// Response is one status response. Content holds the per-content-type
// schema payload.
type Response struct {
	// Content is keyed by MIME type. Empty when the response has
	// no body (e.g. 204 No Content).
	Content map[string]*Schema
}

// Schema is a normalised JSON Schema view. The differ walks
// recursively through Properties, Items, and OneOf/AnyOf when
// present.
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
	// Type is the JSON Schema type string ("object", "string",
	// "integer", "number", "boolean", "array", "null"). Empty
	// for oneOf/anyOf unions.
	Type string
	// Properties is keyed by property name. Nil for non-object
	// schemas.
	Properties map[string]*Schema
	// Required is the sorted required-property list.
	Required []string
	// Items is the array-element schema. Nil for non-array schemas.
	Items *Schema
	// Nullable is true when the schema accepts null. Captures
	// both OpenAPI 3.0 `nullable: true` and OpenAPI 3.1 `[T,
	// 'null']` form per the noise rule.
	Nullable bool
	// OneOf / AnyOf hold union alternatives. Nil when not a
	// union. PR-2 does not walk into these for break detection
	// (kept for completeness; the differ treats any non-nil
	// union as opaque).
	OneOf []*Schema
	AnyOf []*Schema
	// Ref is the unresolved $ref string ("#/components/schemas/Foo").
	// Empty when inlined.
	Ref string
	// Description is the human-readable description. Held for
	// completeness; the differ strips whitespace before any
	// comparison so description-only changes never fire a break.
	Description string
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
// normalised [Spec]. Exposed for tests; production callers should
// use [Load] so the embedded spec is the source of truth.
func LoadBytes(data []byte) (*Spec, error) {
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("openapidiff: parse yaml: %w", err)
	}
	if len(doc) == 0 {
		return nil, errors.New("openapidiff: empty spec")
	}
	spec := &Spec{
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
	pi := &PathItem{Methods: map[string]*Operation{}}
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
	op := &Operation{Responses: map[string]*Response{}}
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
	sch := &Schema{}
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
