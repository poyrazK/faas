package edgevalidate_test

// adr: 091

import (
	"testing"

	edgevalidate "github.com/onebox-faas/faas/pkg/edgevalidate"
)

// reject_on_unknown_fields was stored and documented ("a body with stray
// fields fails") but Compile ignored it, so production-us rc.251 accepted
// {"n":1,"z":2} on a rule with the knob set.
func TestCompileRejectUnknownFields(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, schema, body string
		reject             bool
		field              string
	}{
		{"declared_fields_pass", `{"type":"object","properties":{"n":{"type":"integer"}},"required":["n"]}`, `{"n":1}`, false, ""},
		{"extra_root_field", `{"type":"object","properties":{"n":{"type":"integer"}},"required":["n"]}`, `{"n":1,"z":2}`, true, "/z"},
		{"extra_nested_field", `{"type":"object","properties":{"addr":{"type":"object","properties":{"city":{"type":"string"}}}}}`, `{"addr":{"city":"x","zip":1}}`, true, "/addr/zip"},
		{"extra_field_in_array_item", `{"type":"object","properties":{"rows":{"type":"array","items":{"properties":{"id":{}}}}}}`, `{"rows":[{"id":1,"x":2}]}`, true, "/rows/0/x"},
		{"allof_fields_compose", `{"allOf":[{"properties":{"a":{}}},{"properties":{"b":{}}}]}`, `{"a":1,"b":2}`, false, ""},
		{"allof_extra_field", `{"allOf":[{"properties":{"a":{}}},{"properties":{"b":{}}}]}`, `{"a":1,"c":3}`, true, "/c"},
		{"ref_fields_compose", `{"$defs":{"base":{"properties":{"a":{}}}},"$ref":"#/$defs/base","properties":{"b":{}}}`, `{"a":1,"b":2}`, false, ""},
		{"ref_extra_field", `{"$defs":{"base":{"properties":{"a":{}}}},"$ref":"#/$defs/base","properties":{"b":{}}}`, `{"a":1,"x":1}`, true, "/x"},
		{"explicit_additional_properties_kept", `{"properties":{"a":{}},"additionalProperties":true}`, `{"a":1,"z":1}`, false, ""},
		{"free_form_object_stays_open", `{"properties":{"meta":{"type":"object"}}}`, `{"meta":{"anything":1}}`, false, ""},
		{"free_form_parent_still_closed", `{"properties":{"meta":{"type":"object"}}}`, `{"meta":{},"z":1}`, true, "/z"},
		{"draft07_extra_field", `{"$schema":"http://json-schema.org/draft-07/schema#","properties":{"n":{}}}`, `{"n":1,"z":2}`, true, ""},
		{"draft07_declared_fields_pass", `{"$schema":"http://json-schema.org/draft-07/schema#","properties":{"n":{}}}`, `{"n":1}`, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			open, err := edgevalidate.Compile([]byte(tc.schema), false)
			if err != nil {
				t.Fatalf("Compile(open): %v", err)
			}
			if fe, err := open.Validate([]byte(tc.body)); err != nil || fe != nil {
				t.Fatalf("open schema = %+v, %v; the knob off must keep today's behavior", fe, err)
			}
			closed, err := edgevalidate.Compile([]byte(tc.schema), true)
			if err != nil {
				t.Fatalf("Compile(closed): %v", err)
			}
			if closed.Digest == open.Digest {
				t.Fatal("closed and open compiles share a digest, so the gateway cache would mix them")
			}
			fe, err := closed.Validate([]byte(tc.body))
			if err != nil {
				t.Fatalf("Validate: %v", err)
			}
			if (fe != nil) != tc.reject {
				t.Fatalf("FieldError = %+v; want reject=%v", fe, tc.reject)
			}
			if fe == nil {
				return
			}
			if fe.Reason() != "additional_properties_not_allowed" {
				t.Fatalf("FieldError = %+v reason %q; want additional_properties_not_allowed", fe, fe.Reason())
			}
			if tc.field != "" && fe.Field != tc.field {
				t.Fatalf("Field = %q; want %q", fe.Field, tc.field)
			}
		})
	}
}
