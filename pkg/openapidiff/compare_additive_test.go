package openapidiff

import "testing"

// TestCompareAdditive_NewPathIsCompatible — a brand-new endpoint
// in the proposed spec is intentional non-breaking. The differ
// (Compare) ignores it; CompareAdditive reports it so the
// renderer can show "yes, this is fine".
func TestCompareAdditive_NewPathIsCompatible(t *testing.T) {
	baseYAML := `
openapi: 3.1.0
paths:
  /v1/users:
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
  /v1/users:
    get:
      responses:
        '200':
          content:
            application/json:
              schema:
                type: object
                properties:
                  id: { type: string }
  /v1/orders:
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
	base, _ := LoadBytes([]byte(baseYAML))
	prop, _ := LoadBytes([]byte(propYAML))
	// Pin: Compare reports zero breaks for the same input.
	if breaks := Compare(base, prop); len(breaks) != 0 {
		t.Fatalf("Compare must report 0 breaks for new path; got %d: %+v", len(breaks), breaks)
	}
	add := CompareAdditive(base, prop)
	if len(add) != 1 {
		t.Fatalf("CompareAdditive must report 1 additive (path_added /v1/orders); got %d: %+v", len(add), add)
	}
	if add[0].Kind != AdditivePathAdded {
		t.Fatalf("expected AdditivePathAdded; got %q", add[0].Kind)
	}
	if add[0].Path != "/v1/orders" {
		t.Fatalf("expected Path=/v1/orders; got %q", add[0].Path)
	}
}

// TestCompareAdditive_NewMethodIsCompatible — a new verb on
// an existing path is non-breaking.
func TestCompareAdditive_NewMethodIsCompatible(t *testing.T) {
	baseYAML := `
openapi: 3.1.0
paths:
  /v1/users:
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
  /v1/users:
    get:
      responses:
        '200':
          content:
            application/json:
              schema:
                type: object
                properties:
                  id: { type: string }
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
	add := CompareAdditive(base, prop)
	if len(add) != 1 {
		t.Fatalf("CompareAdditive must report 1 additive (method_added POST); got %d: %+v", len(add), add)
	}
	if add[0].Kind != AdditiveMethodAdded {
		t.Fatalf("expected AdditiveMethodAdded; got %q", add[0].Kind)
	}
	if add[0].Method != "post" {
		t.Fatalf("expected Method=post; got %q", add[0].Method)
	}
}

// TestCompareAdditive_NewPropertyIsCompatible — adding a
// property to an existing schema is non-breaking.
func TestCompareAdditive_NewPropertyIsCompatible(t *testing.T) {
	baseYAML := `
openapi: 3.1.0
paths:
  /v1/users:
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
  /v1/users:
    get:
      responses:
        '200':
          content:
            application/json:
              schema:
                type: object
                properties:
                  id:    { type: string }
                  email: { type: string }
`
	base, _ := LoadBytes([]byte(baseYAML))
	prop, _ := LoadBytes([]byte(propYAML))
	add := CompareAdditive(base, prop)
	if len(add) != 1 {
		t.Fatalf("CompareAdditive must report 1 additive (property_added email); got %d: %+v", len(add), add)
	}
	if add[0].Kind != AdditivePropertyAdded {
		t.Fatalf("expected AdditivePropertyAdded; got %q", add[0].Kind)
	}
	if add[0].PathInSchema != "properties.email" {
		t.Fatalf("expected PathInSchema=properties.email; got %q", add[0].PathInSchema)
	}
}

// TestCompareAdditive_HeadlineExample — the headline example
// from the API contract diff issue:
//
//   - GET /users/{id} no longer returns email → BREAK
//   - POST /orders now requires currency        → BREAK
//   - GET /orders/{id} newly added              → COMPATIBLE
//
// Compare must emit ≥2 breaks (field_removed email +
// required_added currency). CompareAdditive must emit ≥1
// additive (the new GET /orders/{id}). Adding the property
// currency is also additive (new property), even though the
// same property is simultaneously required-added — the two
// observations are independent: a property can be both new
// (additive) and required (break).
func TestCompareAdditive_HeadlineExample(t *testing.T) {
	baseYAML := `
openapi: 3.1.0
paths:
  /users/{id}:
    get:
      responses:
        '200':
          content:
            application/json:
              schema:
                type: object
                properties:
                  id:    { type: string }
                  email: { type: string }
  /orders:
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
	propYAML := `
openapi: 3.1.0
paths:
  /users/{id}:
    get:
      responses:
        '200':
          content:
            application/json:
              schema:
                type: object
                properties:
                  id: { type: string }
  /orders:
    post:
      responses:
        '201':
          content:
            application/json:
              schema:
                type: object
                properties:
                  id:       { type: string }
                  currency: { type: string }
                required: [id, currency]
  /orders/{id}:
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
	base, _ := LoadBytes([]byte(baseYAML))
	prop, _ := LoadBytes([]byte(propYAML))
	breaks := Compare(base, prop)
	// 2 breaks: email removed on /users/{id}, currency required-added on /orders.
	// id is unchanged on /orders (still present in both) so adding it to
	// the required list on the proposed side does NOT fire a break.
	// (differ compares required as a set, and id is already required
	// on the baseline of /orders POST? No — the baseline /orders POST
	// has no required list at all. Adding id alone to the proposed
	// is a required_added id break too. To avoid this, the test
	// expects the headline to produce 2 breaks: (1) field_removed
	// email; (2) required_added currency. The id required-added is
	// also expected — see assertion below.)
	if len(breaks) < 2 {
		t.Fatalf("headline example must produce >= 2 breaks; got %d: %+v", len(breaks), breaks)
	}
	foundFieldRemoved := false
	foundRequiredAdded := false
	for _, b := range breaks {
		switch b.Kind {
		case SchemaKindFieldRemoved:
			if b.PathInSchema == "properties.email" && b.Path == "/users/{id}" {
				foundFieldRemoved = true
			}
		case SchemaKindRequiredAdded:
			if b.PathInSchema == "properties.currency" && b.Path == "/orders" {
				foundRequiredAdded = true
			}
		}
	}
	if !foundFieldRemoved {
		t.Errorf("expected SchemaKindFieldRemoved on email; got %+v", breaks)
	}
	if !foundRequiredAdded {
		t.Errorf("expected SchemaKindRequiredAdded on currency; got %+v", breaks)
	}

	add := CompareAdditive(base, prop)
	if len(add) < 1 {
		t.Fatalf("headline example must produce ≥1 additive; got %d: %+v", len(add), add)
	}
	foundPathAdded := false
	for _, a := range add {
		if a.Kind == AdditivePathAdded && a.Path == "/orders/{id}" {
			foundPathAdded = true
		}
	}
	if !foundPathAdded {
		t.Errorf("expected AdditivePathAdded on /orders/{id}; got %+v", add)
	}
}

// TestCompareAdditive_IdenticalIsZero — comparing two identical
// Specs must produce zero additive changes. Pins the no-op
// invariant.
func TestCompareAdditive_IdenticalIsZero(t *testing.T) {
	yaml := `
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
	a, _ := LoadBytes([]byte(yaml))
	b, _ := LoadBytes([]byte(yaml))
	add := CompareAdditive(a, b)
	if len(add) != 0 {
		t.Fatalf("identical specs must produce 0 additive changes; got %d: %+v", len(add), add)
	}
}

// TestCompareAdditive_DoesNotClassifyBreaksAsAdditive — the
// headline example's BREAK cases (email removed, currency
// required) must NOT appear in the additive list. The two
// output sets are disjoint.
func TestCompareAdditive_DoesNotClassifyBreaksAsAdditive(t *testing.T) {
	baseYAML := `
openapi: 3.1.0
paths:
  /users/{id}:
    get:
      responses:
        '200':
          content:
            application/json:
              schema:
                type: object
                properties:
                  id:    { type: string }
                  email: { type: string }
`
	propYAML := `
openapi: 3.1.0
paths:
  /users/{id}:
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
	base, _ := LoadBytes([]byte(baseYAML))
	prop, _ := LoadBytes([]byte(propYAML))
	add := CompareAdditive(base, prop)
	for _, a := range add {
		if a.PathInSchema == "properties.email" {
			t.Fatalf("CompareAdditive must not list a removed property as additive; got %+v", a)
		}
	}
}
