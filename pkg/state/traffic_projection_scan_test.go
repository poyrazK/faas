// adr: 375
package state

import (
	"encoding/json"
	"testing"
)

func TestMemTrafficProjectionQuotedSeparatorsAndExponents(t *testing.T) {
	for _, test := range []struct {
		name, payload, canonical string
		exponentAllowance        int64
	}{
		{"empty", `""`, `""`, 0},
		{"quoted-separators", `",:e+99"`, `",:e+99"`, 0},
		{"even-backslashes", `{"a":"\\","b":1e+9}`, `{"a": "\\", "b": 1e+9}`, 9},
		{"odd-backslashes", `{"a":"\\\",:e-99","b":1e-9}`, `{"a": "\\\",:e-99", "b": 1e-9}`, 9},
		{"adjacent-escaped-quotes", `["\"\"",null,true,false,1e+3]`, `["\"\"", null, true, false, 1e+3]`, 3},
		{"nested", `{"a":[{"b":"e+99:,"},1e-4],"c":{}}`, `{"a": [{"b": "e+99:,"}, 1e-4], "c": {}}`, 4},
		{"html-escape-bound", `["&<>"]`, `["\u0026\u003c\u003e"]`, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := memTrafficProjectionSize("test", json.RawMessage(test.payload))
			want := int64(len(test.canonical)) + test.exponentAllowance
			if err != nil || got != want {
				t.Fatalf("canonical upper bound: got=%d want=%d err=%v", got, want, err)
			}
		})
	}
}
