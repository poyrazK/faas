package edgevalidate_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/edgevalidate"
)

func TestRegressionStrictComposedDrafts(t *testing.T) {
	for _, draft := range []string{"http://json-schema.org/draft-07/schema#", "https://json-schema.org/draft/2020-12/schema"} {
		t.Run(draft, func(t *testing.T) {
			schema := []byte(`{"$schema":"` + draft + `","type":"object","allOf":[{"properties":{"name":{"type":"string"}},"required":["name"]},{"properties":{"n":{"type":"integer"}},"required":["n"]}]}`)
			compiled, err := edgevalidate.Compile(schema, true)
			if err != nil {
				t.Fatal(err)
			}
			valid, err := compiled.Validate([]byte(`{"name":"ok","n":1}`))
			if err != nil || valid != nil {
				t.Fatalf("valid composed-object control: %v, %v", valid, err)
			}
			unknown, err := compiled.Validate([]byte(`{"name":"ok","n":1,"admin":true}`))
			if err != nil {
				t.Fatal(err)
			}
			if unknown == nil {
				t.Error("strict validation admitted undeclared admin field")
			}
		})
	}
}

func TestStrictLegacyCompositionPreservesDialect(t *testing.T) {
	for _, tc := range []struct{ name, schema, valid, invalid string }{
		{"reference", `{"definitions":{"obj":{"properties":{"name":{}}}},"$ref":"#/definitions/obj","required":["ignored-sibling"]}`, `{"name":"ok"}`, `{"name":"ok","extra":true}`},
		{"nested", `{"properties":{"item":{"allOf":[{"properties":{"a":{}}},{"properties":{"b":{}}}]}}}`, `{"item":{"a":1,"b":2}}`, `{"item":{"a":1,"extra":2}}`},
		{"anyof", `{"anyOf":[{"properties":{"a":{}},"required":["a"]},{"properties":{"b":{}},"required":["b"]}]}`, `{"a":1}`, `{"a":1,"extra":2}`},
		{"tuple", `{"type":"array","items":[{"allOf":[{"properties":{"a":{}}},{"properties":{"b":{}}}]}],"additionalItems":false}`, `[{"a":1,"b":2}]`, `[{"a":1,"b":2},{}]`},
		{"additional_schema", `{"additionalProperties":{"allOf":[{"properties":{"a":{}}},{"properties":{"b":{}}}]}}`, `{"one":{"a":1,"b":2}}`, `{"one":{"a":1,"extra":2}}`},
		{"conditional", `{"properties":{"kind":{}},"if":{"properties":{"kind":{"const":"a"}}},"then":{"properties":{"a":{}}},"else":{"properties":{"b":{}}}}`, `{"kind":"a","a":1}`, `{"kind":"a","b":1}`},
		{"recursive", `{"definitions":{"node":{"properties":{"value":{},"child":{"$ref":"#/definitions/node"}}}},"$ref":"#/definitions/node"}`, `{"value":1,"child":{"value":2}}`, `{"value":1,"child":{"extra":2}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			schema := []byte(`{"$schema":"http://json-schema.org/draft-07/schema#",` + tc.schema[1:])
			c, err := edgevalidate.Compile(schema, true)
			if err != nil {
				t.Fatal(err)
			}
			if field, err := c.Validate([]byte(tc.valid)); err != nil || field != nil {
				t.Fatalf("valid input rejected: %+v %v", field, err)
			}
			if field, err := c.Validate([]byte(tc.invalid)); err != nil || field == nil {
				t.Fatalf("invalid input admitted: %+v %v", field, err)
			}
		})
	}
	for _, schema := range []string{`{"type":"object"}`, `{"allOf":[{"properties":{"a":{}}}],"additionalProperties":true}`} {
		c, err := edgevalidate.Compile([]byte(`{"$schema":"http://json-schema.org/draft-07/schema#",`+schema[1:]), true)
		if err != nil {
			t.Fatal(err)
		}
		if field, err := c.Validate([]byte(`{"extra":true}`)); field != nil || err != nil {
			t.Fatalf("explicit/free-form object changed: %+v %v", field, err)
		}
	}
}
