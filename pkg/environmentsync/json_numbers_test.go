package environmentsync

import (
	"encoding/json"
	"testing"
)

func TestCanonicalJSONNumbersPreservePrecisionAndSurviveStorage(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"1e3", "1000"}, {"1000.000", "1000"}, {"1E+03", "1000"},
		{"-0.0000", "0"}, {"0.0000010", "0.000001"},
		{"0.00000010", "1e-7"}, {"1000000000000000000000", "1e+21"},
		{"9007199254740993", "9007199254740993"},
		{"1.2345678901234567890123456789e-50", "1.2345678901234567890123456789e-50"},
		{`{"z":[1e-7,9007199254740993],"x":{"count":1e3}}`, `{"x":{"count":1000},"z":[1e-7,9007199254740993]}`},
	} {
		t.Run(tc.input, func(t *testing.T) {
			got, err := canonicalJSON([]byte(tc.input))
			if err != nil || string(got) != tc.want {
				t.Fatalf("canonical=%s error=%v, want %s", got, err, tc.want)
			}
			again, err := canonicalJSON(got)
			if err != nil || string(again) != string(got) {
				t.Fatalf("canonicalization changed on repeat: %s %v", again, err)
			}
		})
	}
}

func TestDefinitionDigestSurvivesJSONBNumericRepresentation(t *testing.T) {
	d := definition()
	d.Configuration = map[string]json.RawMessage{"RATE": json.RawMessage(`1e-7`), "COUNT": json.RawMessage(`9007199254740993`)}
	before, err := Compile(d)
	if err != nil {
		t.Fatal(err)
	}
	d.Configuration["RATE"] = json.RawMessage(`0.0000001`)
	after, err := Compile(d)
	if err != nil || before.Digest != after.Digest {
		t.Fatalf("jsonb normalization changed identity: %s -> %s, %v", before.Digest, after.Digest, err)
	}
	if string(after.Definition.Configuration["COUNT"]) != "9007199254740993" {
		t.Fatal("large integer precision was lost")
	}
}
