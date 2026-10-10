package dataapi

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func wrapSnapshot(t *testing.T, raw string) []byte {
	t.Helper()
	content, err := json.Marshal(Contract{Version: 1, Fingerprint: fmt.Sprintf("%x", sha256.Sum256([]byte(raw))), Snapshot: json.RawMessage(raw)})
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func TestContractResultRelationMetadata(t *testing.T) {
	row := func(name string) Table {
		return Table{Schema: "api", Name: name, Columns: []Column{{Name: "id", Type: "number"}}, Relationships: json.RawMessage(`[]`)}
	}
	snapshot := Snapshot{Version: 3, Schemas: []string{"api"}, Tables: []Table{row("orders"), row("invoices")},
		Functions: []Function{{Schema: "api", Name: "latest_invoice", Args: []Argument{{Name: "source_row", Type: `Database["api"]["Tables"]["orders"]['Row']`}},
			Returns: `Database["api"]["Tables"]["invoices"]['Row'][]`, Setof: &Setof{From: "orders", To: "invoices", OneToOne: true}}},
		Enums: map[string][]string{}, Composites: []Composite{}}
	content, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	base := string(content)
	for _, tc := range []struct {
		name, old, next string
		valid           bool
	}{
		{"computed", "", "", true},
		{"legacy v2 rejects metadata", `"version":3`, `"version":2`, false},
		{"future version", `"version":3`, `"version":4`, false},
		{"unknown target", `"to":"invoices"`, `"to":"private"`, false},
		{"unknown source", `"from":"orders"`, `"from":"private"`, false},
		{"missing cardinality", `,"one_to_one":true`, ``, false},
		{"null cardinality", `"one_to_one":true`, `"one_to_one":null`, false},
		{"unknown metadata", `"one_to_one":true`, `"one_to_one":true,"sql":"hidden"`, false},
		{"non-set RPC", `['Row'][]`, `['Row']`, false},
		{"optional source", `"optional":false`, `"optional":true`, false},
		{"name shadows relation", `"name":"latest_invoice"`, `"name":"orders"`, false},
		{"name shadows column", `"name":"latest_invoice"`, `"name":"id"`, false},
		{"RPC-only cannot imply a singular transport", `"from":"orders"`, `"from":""`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := base
			if tc.old != "" {
				raw = strings.ReplaceAll(raw, tc.old, tc.next)
			}
			parsed, err := Parse(wrapSnapshot(t, raw))
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
			if tc.valid {
				encoded, err := json.Marshal(parsed.Parsed)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := Parse(wrapSnapshot(t, string(encoded))); err != nil {
					t.Fatalf("round trip: %v", err)
				}
			}
		})
	}
	ordinary := strings.ReplaceAll(strings.ReplaceAll(base, `"from":"orders"`, `"from":""`), `"one_to_one":true`, `"one_to_one":false`)
	if _, err := Parse(wrapSnapshot(t, ordinary)); err != nil {
		t.Fatalf("ordinary result-set metadata: %v", err)
	}
}

func TestContractVersionsAndCompositeValidation(t *testing.T) {
	root := `"schemas":["api"],"tables":[],"functions":[],"enums":{}`
	composite := `{"schema":"api","name":"location","fields":[{"name":"x","type":"number","nullable":true}]}`
	for _, tc := range []struct {
		name, raw string
		valid     bool
	}{
		{"legacy", `{"version":1,` + root + `}`, true},
		{"empty v2", `{"version":2,` + root + `,"composites":[]}`, true},
		{"empty v3", `{"version":3,` + root + `,"composites":[]}`, true},
		{"composite", `{"version":2,` + root + `,"composites":[` + composite + `]}`, true},
		{"missing v2 types", `{"version":2,` + root + `}`, false},
		{"null v2 types", `{"version":2,` + root + `,"composites":null}`, false},
		{"v1 cannot carry new metadata", `{"version":1,` + root + `,"composites":[]}`, false},
		{"future version", `{"version":4,` + root + `,"composites":[]}`, false},
		{"duplicate type", `{"version":2,` + root + `,"composites":[` + composite + `,` + composite + `]}`, false},
		{"hidden schema", `{"version":2,` + root + `,"composites":[{"schema":"private","name":"hidden","fields":[]}]}`, false},
		{"empty type", `{"version":2,` + root + `,"composites":[{"schema":"api","name":"empty","fields":[]}]}`, true},
		{"null fields", `{"version":2,` + root + `,"composites":[{"schema":"api","name":"empty","fields":null}]}`, false},
		{"missing nullability", `{"version":2,` + root + `,"composites":[{"schema":"api","name":"x","fields":[{"name":"x","type":"number"}]}]}`, false},
		{"null nullability", `{"version":2,` + root + `,"composites":[{"schema":"api","name":"x","fields":[{"name":"x","type":"number","nullable":null}]}]}`, false},
		{"duplicate field", `{"version":2,` + root + `,"composites":[{"schema":"api","name":"x","fields":[{"name":"x","type":"number","nullable":true},{"name":"x","type":"number","nullable":true}]}]}`, false},
		{"unknown metadata", `{"version":2,` + root + `,"composites":[{"schema":"api","name":"x","fields":[],"sql":"hidden"}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			contract, err := Parse(wrapSnapshot(t, tc.raw))
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
			if tc.valid {
				raw, err := json.Marshal(contract.Parsed)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := Parse(wrapSnapshot(t, string(raw))); err != nil {
					t.Fatalf("round trip: %v", err)
				}
			}
		})
	}
}
