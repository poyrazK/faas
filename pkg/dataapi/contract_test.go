package dataapi

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
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

func TestContractVersionsAndCompositeValidation(t *testing.T) {
	root := `"schemas":["api"],"tables":[],"functions":[],"enums":{}`
	composite := `{"schema":"api","name":"location","fields":[{"name":"x","type":"number","nullable":true}]}`
	for _, tc := range []struct {
		name, raw string
		valid     bool
	}{
		{"legacy", `{"version":1,` + root + `}`, true},
		{"empty v2", `{"version":2,` + root + `,"composites":[]}`, true},
		{"composite", `{"version":2,` + root + `,"composites":[` + composite + `]}`, true},
		{"missing v2 types", `{"version":2,` + root + `}`, false},
		{"null v2 types", `{"version":2,` + root + `,"composites":null}`, false},
		{"v1 cannot carry new metadata", `{"version":1,` + root + `,"composites":[]}`, false},
		{"future version", `{"version":3,` + root + `,"composites":[]}`, false},
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
