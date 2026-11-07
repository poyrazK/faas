package edgevalidate_test

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/edgevalidate"
)

// adr: 20261107190000 — a body that names a property twice,
// exactly or in two spellings that differ only in case, fails a validate
// rule. Go's encoding/json and ASP.NET Core bind names case-insensitively, so
// a second spelling would reach the app past the schema.
func TestValidateRefusesDuplicateAndCaseVariantKeys(t *testing.T) {
	schema := []byte(`{"type":"object","properties":{"role":{"type":"string","enum":["user"]},"amount":{"type":"number","maximum":100},"items":{"type":"array"}},"required":["role"]}`)
	c, err := edgevalidate.Compile(schema, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, body, field string
	}{
		{"clean body", `{"role":"user","amount":50}`, ""},
		{"case variant of a constrained key", `{"role":"user","ROLE":"admin"}`, "/ROLE"},
		{"case variant of a bounded number", `{"role":"user","amount":50,"Amount":1000000}`, "/Amount"},
		{"exact duplicate", `{"role":"admin","role":"user"}`, "/role"},
		{"nested object", `{"role":"user","items":[{"id":1,"ID":2}]}`, "/items/0/ID"},
		{"distinct keys that share a prefix", `{"role":"user","roles":["a"]}`, ""},
		// encoding/json folds U+017F (long s) to "s" and U+212A (Kelvin) to "k".
		{"long s folds to s", "{\"role\":\"user\",\"amount\":1,\"items\":[{\"status\":1,\"ſtatus\":2}]}", "/items/0/ſtatus"},
		{"Kelvin sign folds to k", "{\"role\":\"user\",\"items\":[{\"kind\":1,\"Kind\":2}]}", "/items/0/Kind"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fe, err := c.Validate([]byte(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			switch {
			case tc.field == "" && fe != nil:
				t.Fatalf("Validate(%s) refused: %+v", tc.body, fe)
			case tc.field != "" && (fe == nil || fe.Field != tc.field):
				t.Fatalf("Validate(%s) = %+v, want a refusal at %s", tc.body, fe, tc.field)
			}
		})
	}
	// What the refused case-variant body would have meant to a Go app.
	var app struct {
		Role string `json:"role"`
	}
	if err := json.Unmarshal([]byte(`{"role":"user","ROLE":"admin"}`), &app); err != nil || app.Role != "admin" {
		t.Fatalf("encoding/json no longer binds case-insensitively (role=%q); revisit this check", app.Role)
	}
}
