package openapidiff

import (
	"strings"
	"testing"
)

// TestCompare_PropertyReorderIsNoise — reordering properties on
// the same schema must NOT fire a break. The loader sorts
// property names at every level, so the differ never sees the
// original order. This pins the noise rule.
func TestCompare_PropertyReorderIsNoise(t *testing.T) {
	baseYAML := `
openapi: 3.1.0
paths:
  /v1/things:
    get:
      responses:
        '200':
          content:
            application/json:
              schema:
                type: object
                properties:
                  alpha: { type: string }
                  beta:  { type: integer }
                  gamma: { type: boolean }
`
	propYAML := strings.Replace(baseYAML,
		"                  alpha: { type: string }\n                  beta:  { type: integer }\n                  gamma: { type: boolean }",
		"                  gamma: { type: boolean }\n                  alpha: { type: string }\n                  beta:  { type: integer }",
		1)
	base, err := LoadBytes([]byte(baseYAML))
	if err != nil {
		t.Fatalf("baseline parse: %v", err)
	}
	prop, err := LoadBytes([]byte(propYAML))
	if err != nil {
		t.Fatalf("proposed parse: %v", err)
	}
	breaks := Compare(base, prop)
	if len(breaks) != 0 {
		t.Fatalf("property reorder must produce 0 breaks; got %d: %+v", len(breaks), breaks)
	}
}

// TestCompare_OpenAPI31_NullableArray — the OpenAPI 3.1
// `[T, 'null']` form is semantically equal to `nullable: true`
// (per memory [pr-819-openapi-nullable-3-1]). The loader
// collapses both to Schema.Nullable, so the differ sees no
// difference. This pins the noise rule.
func TestCompare_OpenAPI31_NullableArray(t *testing.T) {
	yamlBool := `
openapi: 3.1.0
paths:
  /v1/things:
    get:
      responses:
        '200':
          content:
            application/json:
              schema:
                type: object
                properties:
                  name:
                    type: string
                    nullable: true
`
	yamlArr := `
openapi: 3.1.0
paths:
  /v1/things:
    get:
      responses:
        '200':
          content:
            application/json:
              schema:
                type: object
                properties:
                  name:
                    type: [string, 'null']
`
	base, err := LoadBytes([]byte(yamlBool))
	if err != nil {
		t.Fatalf("baseline parse: %v", err)
	}
	prop, err := LoadBytes([]byte(yamlArr))
	if err != nil {
		t.Fatalf("proposed parse: %v", err)
	}
	breaks := Compare(base, prop)
	if len(breaks) != 0 {
		t.Fatalf("nullable:true vs [string,'null'] must produce 0 breaks; got %d: %+v", len(breaks), breaks)
	}
}

// TestCompare_TypeChange_FiresBreak — changing a property's
// type is the canonical structural break and must fire
// exactly one SchemaBreak anchored to the property path.
func TestCompare_TypeChange_FiresBreak(t *testing.T) {
	baseYAML := `
openapi: 3.1.0
paths:
  /v1/things:
    get:
      responses:
        '200':
          content:
            application/json:
              schema:
                type: object
                properties:
                  count:
                    type: integer
`
	propYAML := `
openapi: 3.1.0
paths:
  /v1/things:
    get:
      responses:
        '200':
          content:
            application/json:
              schema:
                type: object
                properties:
                  count:
                    type: string
`
	base, err := LoadBytes([]byte(baseYAML))
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}
	prop, err := LoadBytes([]byte(propYAML))
	if err != nil {
		t.Fatalf("proposed: %v", err)
	}
	breaks := Compare(base, prop)
	if len(breaks) != 1 {
		t.Fatalf("expected 1 break; got %d: %+v", len(breaks), breaks)
	}
	b := breaks[0]
	if b.Kind != SchemaKindTypeChange {
		t.Fatalf("expected SchemaKindTypeChange; got %q", b.Kind)
	}
	if b.Path != "/v1/things" || b.Method != "get" || b.Status != "200" {
		t.Fatalf("unexpected anchor: %+v", b)
	}
	if b.PathInSchema != "properties.count" {
		t.Fatalf("expected PathInSchema=properties.count; got %q", b.PathInSchema)
	}
	if b.Before != "integer" || b.After != "string" {
		t.Fatalf("expected before=integer after=string; got before=%v after=%v", b.Before, b.After)
	}
}

func TestCompareDetailedReportsChangedResponseUnionAsUnknown(t *testing.T) {
	baseline := `{"openapi":"3.1.0","paths":{"/items":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"object","properties":{"value":{"oneOf":[{"type":"string"},{"type":"integer"}]}}}}}}}}}}}`
	changed := `{"openapi":"3.1.0","paths":{"/items":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"object","properties":{"value":{"oneOf":[{"type":"string"},{"type":"boolean"}]}}}}}}}}}}}`
	reordered := `{"openapi":"3.1.0","paths":{"/items":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"object","properties":{"value":{"oneOf":[{"type":"integer"},{"type":"string"}]}}}}}}}}}}}`

	base, err := LoadBytes([]byte(baseline))
	if err != nil {
		t.Fatal(err)
	}
	changedSpec, err := LoadBytes([]byte(changed))
	if err != nil {
		t.Fatal(err)
	}
	reorderedSpec, err := LoadBytes([]byte(reordered))
	if err != nil {
		t.Fatal(err)
	}

	comparison := CompareDetailed(base, changedSpec)
	if len(comparison.Breaks) != 0 || len(comparison.Unknowns) != 1 {
		t.Fatalf("changed union comparison = %+v, want one unknown and no confirmed break", comparison)
	}
	unknown := comparison.Unknowns[0]
	if unknown.Path != "/items" || unknown.Method != "get" || unknown.Status != "200" ||
		unknown.PathInSchema != "properties.value" || unknown.Code != SchemaUnknownUnsupportedUnionChange {
		t.Fatalf("unknown finding = %+v, want the changed union location", unknown)
	}

	comparison = CompareDetailed(base, reorderedSpec)
	if len(comparison.Breaks) != 0 || len(comparison.Unknowns) != 0 {
		t.Fatalf("reordering union alternatives should be noise, got %+v", comparison)
	}
}

func TestCompareDetailedReportsUnsupportedResponseFacetChangesAsUnknown(t *testing.T) {
	baseline := `{"openapi":"3.1.0","paths":{"/items":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"object","properties":{"state":{"type":"string","enum":["ready","failed"]},"id":{"type":"string","format":"uuid"}}}}}}}}}}}`
	changed := `{"openapi":"3.1.0","paths":{"/items":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"object","properties":{"state":{"type":"string","enum":["ready","pending"]},"id":{"type":"string","format":"uuid"}}}}}}}}}}}`
	unchanged := `{"openapi":"3.1.0","paths":{"/items":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"object","properties":{"state":{"type":"string","enum":["ready","failed"]},"id":{"type":"string","format":"uuid"}}}}}}}}}}}`

	load := func(document string) *Spec {
		t.Helper()
		spec, err := LoadBytes([]byte(document))
		if err != nil {
			t.Fatal(err)
		}
		return spec
	}
	base := load(baseline)

	comparison := CompareDetailed(base, load(changed))
	if len(comparison.Breaks) != 0 || len(comparison.Unknowns) != 1 {
		t.Fatalf("changed response enum comparison = %+v, want one unknown and no confirmed break", comparison)
	}
	unknown := comparison.Unknowns[0]
	if unknown.Path != "/items" || unknown.Method != "get" || unknown.Status != "200" ||
		unknown.PathInSchema != "properties.state" || unknown.Code != SchemaUnknownUnsupportedSchemaChange {
		t.Fatalf("unknown finding = %+v, want the changed enum property location", unknown)
	}

	comparison = CompareDetailed(base, load(unchanged))
	if len(comparison.Breaks) != 0 || len(comparison.Unknowns) != 0 {
		t.Fatalf("unchanged unsupported facets should be noise, got %+v", comparison)
	}
}

func TestCompareDetailedReportsUnsupportedItemFacetChangesAsUnknown(t *testing.T) {
	baseline := `{"openapi":"3.1.0","paths":{"/items":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"array","items":{"type":"string","format":"uuid"}}}}}}}}}}`
	changed := `{"openapi":"3.1.0","paths":{"/items":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"array","items":{"type":"string","format":"date-time"}}}}}}}}}}`
	base, err := LoadBytes([]byte(baseline))
	if err != nil {
		t.Fatal(err)
	}
	prop, err := LoadBytes([]byte(changed))
	if err != nil {
		t.Fatal(err)
	}
	comparison := CompareDetailed(base, prop)
	if len(comparison.Breaks) != 0 || len(comparison.Unknowns) != 1 ||
		comparison.Unknowns[0].PathInSchema != "items" ||
		comparison.Unknowns[0].Code != SchemaUnknownUnsupportedSchemaChange {
		t.Fatalf("item format comparison = %+v, want one unknown at items", comparison)
	}
}

// TestCompare_RequiredAdded_FiresBreak keeps the established breaking
// classification when a response property becomes guaranteed in the
// published contract.
func TestCompare_RequiredAdded_FiresBreak(t *testing.T) {
	baseYAML := `
openapi: 3.1.0
paths:
  /v1/things:
    post:
      responses:
        '201':
          content:
            application/json:
              schema:
                type: object
                properties:
                  id: { type: string }
                required: [id]
`
	propYAML := `
openapi: 3.1.0
paths:
  /v1/things:
    post:
      responses:
        '201':
          content:
            application/json:
              schema:
                type: object
                properties:
                  id:    { type: string }
                  email: { type: string }
                required: [id, email]
`
	base, err := LoadBytes([]byte(baseYAML))
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}
	prop, err := LoadBytes([]byte(propYAML))
	if err != nil {
		t.Fatalf("proposed: %v", err)
	}
	breaks := Compare(base, prop)
	if len(breaks) != 1 {
		t.Fatalf("expected exactly 1 break (required_added on email); got %d: %+v", len(breaks), breaks)
	}
	b := breaks[0]
	if b.Kind != SchemaKindRequiredAdded {
		t.Fatalf("expected SchemaKindRequiredAdded; got %q", b.Kind)
	}
	if b.After != "email" {
		t.Fatalf("expected After=email; got %v", b.After)
	}
}

// TestCompare_FieldRemoved_FiresBreak — removing a property
// from a schema is a break; clients that read the field will
// see null/undefined and may 500 on assumptions. (Adding a
// property is NOT a break — JSON Schema tolerates unknowns.)
func TestCompare_FieldRemoved_FiresBreak(t *testing.T) {
	baseYAML := `
openapi: 3.1.0
paths:
  /v1/things:
    get:
      responses:
        '200':
          content:
            application/json:
              schema:
                type: object
                properties:
                  id:   { type: string }
                  name: { type: string }
                required: [id]
`
	propYAML := `
openapi: 3.1.0
paths:
  /v1/things:
    get:
      responses:
        '200':
          content:
            application/json:
              schema:
                type: object
                properties:
                  id: { type: string }
                required: [id]
`
	base, err := LoadBytes([]byte(baseYAML))
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}
	prop, err := LoadBytes([]byte(propYAML))
	if err != nil {
		t.Fatalf("proposed: %v", err)
	}
	breaks := Compare(base, prop)
	if len(breaks) != 1 {
		t.Fatalf("expected 1 break (field_removed on name); got %d: %+v", len(breaks), breaks)
	}
	b := breaks[0]
	if b.Kind != SchemaKindFieldRemoved {
		t.Fatalf("expected SchemaKindFieldRemoved; got %q", b.Kind)
	}
	if b.PathInSchema != "properties.name" {
		t.Fatalf("expected PathInSchema=properties.name; got %q", b.PathInSchema)
	}
	if b.Before != "name" {
		t.Fatalf("expected Before=name; got %v", b.Before)
	}
}

// TestCompare_PropertyAdded_NotABreak — adding a property to
// an existing schema is intentionally NOT a break. JSON Schema
// clients ignore unknown properties by default, and the OpenAPI
// "additionalProperties" default is permissive. The differ
// must stay silent here.
func TestCompare_PropertyAdded_NotABreak(t *testing.T) {
	baseYAML := `
openapi: 3.1.0
paths:
  /v1/things:
    get:
      responses:
        '200':
          content:
            application/json:
              schema:
                type: object
                properties:
                  id: { type: string }
`
	propYAML := `
openapi: 3.1.0
paths:
  /v1/things:
    get:
      responses:
        '200':
          content:
            application/json:
              schema:
                type: object
                properties:
                  id:   { type: string }
                  name: { type: string }
`
	base, _ := LoadBytes([]byte(baseYAML))
	prop, _ := LoadBytes([]byte(propYAML))
	breaks := Compare(base, prop)
	if len(breaks) != 0 {
		t.Fatalf("property add must not break; got %d: %+v", len(breaks), breaks)
	}
}

// TestCompare_RequiredRemoved_FiresBreak catches a lost response guarantee:
// clients can no longer rely on this field being present.
func TestCompare_RequiredRemoved_FiresBreak(t *testing.T) {
	baseYAML := `
openapi: 3.1.0
paths:
  /v1/things:
    post:
      responses:
        '201':
          content:
            application/json:
              schema:
                type: object
                properties:
                  id: { type: string }
                required: [id]
`
	propYAML := `
openapi: 3.1.0
paths:
  /v1/things:
    post:
      responses:
        '201':
          content:
            application/json:
              schema:
                type: object
                properties:
                  id: { type: string }
`
	base, _ := LoadBytes([]byte(baseYAML))
	prop, _ := LoadBytes([]byte(propYAML))
	breaks := Compare(base, prop)
	if len(breaks) != 1 {
		t.Fatalf("required→optional must emit one response compatibility finding; got %d: %+v", len(breaks), breaks)
	}
	if breaks[0].Kind != SchemaKindRequiredRemoved || breaks[0].PathInSchema != "properties.id" {
		t.Fatalf("required→optional finding = %+v", breaks[0])
	}
}

// TestCompare_NullabilityFlip_FiresBreak — a real nullability
// flip (nullable:true → nullable:false, or vice versa) IS a
// break. The noise rule only collapses the two equivalent
// *syntactic* forms; an actual semantic flip is what the
// differ is for.
func TestCompare_NullabilityFlip_FiresBreak(t *testing.T) {
	baseYAML := `
openapi: 3.1.0
paths:
  /v1/things:
    get:
      responses:
        '200':
          content:
            application/json:
              schema:
                type: object
                properties:
                  name:
                    type: string
                    nullable: true
`
	propYAML := `
openapi: 3.1.0
paths:
  /v1/things:
    get:
      responses:
        '200':
          content:
            application/json:
              schema:
                type: object
                properties:
                  name:
                    type: string
`
	base, _ := LoadBytes([]byte(baseYAML))
	prop, _ := LoadBytes([]byte(propYAML))
	breaks := Compare(base, prop)
	if len(breaks) != 1 {
		t.Fatalf("expected 1 break (nullability_change); got %d: %+v", len(breaks), breaks)
	}
	if breaks[0].Kind != SchemaKindNullabilityChange {
		t.Fatalf("expected SchemaKindNullabilityChange; got %q", breaks[0].Kind)
	}
	if breaks[0].PathInSchema != "properties.name" {
		t.Fatalf("expected PathInSchema=properties.name; got %q", breaks[0].PathInSchema)
	}
}

// TestCompare_DescriptionWhitespaceIsNoise — description text
// changing whitespace only must not fire a break. The loader
// collapses all whitespace to single spaces so the differ
// sees identical Description fields.
func TestCompare_DescriptionWhitespaceIsNoise(t *testing.T) {
	baseYAML := `
openapi: 3.1.0
paths:
  /v1/things:
    get:
      responses:
        '200':
          description: |
            A
            multi-line
            description.
          content:
            application/json:
              schema:
                type: object
                properties:
                  id: { type: string }
`
	propYAML := strings.Replace(baseYAML, "A\n            multi-line\n            description.",
		"A multi-line description.", 1)
	base, _ := LoadBytes([]byte(baseYAML))
	prop, _ := LoadBytes([]byte(propYAML))
	breaks := Compare(base, prop)
	if len(breaks) != 0 {
		t.Fatalf("description whitespace must not break; got %d: %+v", len(breaks), breaks)
	}
}

// TestCompare_RefResolution — a $ref to a component schema
// must walk through the ref. Changing the referenced schema's
// type must produce a break at the operation that referenced
// it, not at the component itself (the differ is anchored to
// operations, not components).
func TestCompare_RefResolution(t *testing.T) {
	baseYAML := `
openapi: 3.1.0
paths:
  /v1/things:
    get:
      responses:
        '200':
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Thing'
components:
  schemas:
    Thing:
      type: object
      properties:
        id: { type: string }
`
	propYAML := `
openapi: 3.1.0
paths:
  /v1/things:
    get:
      responses:
        '200':
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Thing'
components:
  schemas:
    Thing:
      type: object
      properties:
        id: { type: integer }
`
	base, _ := LoadBytes([]byte(baseYAML))
	prop, _ := LoadBytes([]byte(propYAML))
	breaks := Compare(base, prop)
	if len(breaks) != 1 {
		t.Fatalf("expected 1 break (type_change on Thing.properties.id via $ref); got %d: %+v", len(breaks), breaks)
	}
	b := breaks[0]
	if b.Kind != SchemaKindTypeChange {
		t.Fatalf("expected SchemaKindTypeChange; got %q", b.Kind)
	}
	if b.PathInSchema != "properties.id" {
		t.Fatalf("expected PathInSchema=properties.id (resolved through $ref); got %q", b.PathInSchema)
	}
}

// TestCompare_EmbeddedSpec_LoadsClean — the embedded
// pkg/apid/openapi.yaml must load cleanly through Load() with
// zero error. A failure here means the loader has drifted from
// the spec's actual shape (likely a new OpenAPI 3.1 construct
// we don't handle yet).
func TestCompare_EmbeddedSpec_LoadsClean(t *testing.T) {
	s, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if s.OpenAPIVersion() == "" {
		t.Fatalf("OpenAPIVersion() empty")
	}
	// Sanity: every path has at least one method.
	for pathKey, pi := range s.Paths {
		if len(pi.Methods) == 0 {
			t.Fatalf("path %q has no methods", pathKey)
		}
	}
}

// TestCompare_EmbeddedSpec_IdenticalIsZero — comparing the
// embedded spec against itself must produce zero breaks. Pins
// the deterministic-walk invariant: a no-op deploy is silent.
func TestCompare_EmbeddedSpec_IdenticalIsZero(t *testing.T) {
	base, err := Load()
	if err != nil {
		t.Fatalf("Load() baseline: %v", err)
	}
	prop, err := Load()
	if err != nil {
		t.Fatalf("Load() proposed: %v", err)
	}
	breaks := Compare(base, prop)
	if len(breaks) != 0 {
		t.Fatalf("identical specs must produce 0 breaks; got %d", len(breaks))
	}
}
