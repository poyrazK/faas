package functionschema

import (
	"strings"
	"testing"
)

const testSchema = `{"type":"object","required":["email"],"additionalProperties":false,
"properties":{"email":{"type":"string","pattern":"@","maxLength":20},"age":{"type":"integer","minimum":0},
"tags":{"type":"array","maxItems":2,"items":{"enum":["a","b"]}},"score":{"type":["number","null"],"exclusiveMaximum":10}}}`

// The same cases and messages as the Node and Python adapters
// (guest/runners/internal/function_input_test.go), so every runtime refuses
// the same calls the same way.
func TestCheckBodyMatchesTheAdapters(t *testing.T) {
	s, err := Parse([]byte(testSchema))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ body, want string }{
		{`{"email":"a@b","age":3,"tags":["a"],"score":null}`, ""},
		{`{"email":"a@b","score":9.5}`, ""},
		{`{}`, "input.email is required"},
		{`{"email":"a@b","age":1.5}`, "input.age must be integer"},
		{`{"email":"a@b","age":-1}`, "input.age must be >= 0"},
		{`{"email":"nope"}`, "input.email must match @"},
		{`{"email":"a@b","x":1}`, "input.x is not allowed"},
		{`{"email":"a@b","tags":["c"]}`, `input.tags[0] must be one of ["a","b"]`},
		{`{"email":"a@b","tags":["a","b","a"]}`, "input.tags must have at most 2 items"},
		{`{"email":"a@b","score":10}`, "input.score must be < 10"},
		{`{"email":"aaaaaaaaaaaaaaaaaaaa@b"}`, "input.email must be at most 20 characters"},
		{`{`, "input must be JSON"},
		{``, "input must be object"},
		{`[]`, "input must be object"},
	} {
		if got := s.CheckBody([]byte(tc.body)); !strings.HasPrefix(got, tc.want) || (tc.want == "" && got != "") {
			t.Errorf("CheckBody(%s) = %q, want %q", tc.body, got, tc.want)
		}
	}
}

func TestUnknownKeywordsAreIgnored(t *testing.T) {
	s, err := Parse([]byte(`{"type":"object","properties":{"id":{"type":"string","format":"uuid","$ref":"#/x"}},"oneOf":[{}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := s.CheckBody([]byte(`{"id":"not-a-uuid"}`)); got != "" {
		t.Fatalf("unsupported keywords refused a call: %q", got)
	}
}
