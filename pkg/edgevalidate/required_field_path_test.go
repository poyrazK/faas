package edgevalidate_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/edgevalidate"
)

// Hunt 32 H32-60: a sample missing a required root field printed
// "required_missing | field " — the failure is located at the object that
// lacks the property, so the field was empty. It now names the property.
func TestRequiredFailureNamesTheMissingField(t *testing.T) {
	for _, tc := range []struct {
		name, schema, body, want string
	}{
		{"root", `{"type":"object","required":["id"]}`, `{}`, "/id"},
		{"nested", `{"type":"object","properties":{"address":{"type":"object","required":["zip"]}}}`, `{"address":{}}`, "/address/zip"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			compiled, err := edgevalidate.Compile([]byte(tc.schema), false)
			if err != nil {
				t.Fatal(err)
			}
			fe, err := compiled.Validate([]byte(tc.body))
			if err != nil || fe == nil {
				t.Fatalf("Validate = %+v, %v; want a required failure", fe, err)
			}
			if fe.Reason() != "required_missing" || fe.Field != tc.want {
				t.Fatalf("FieldError = %+v; want required_missing at %q", fe, tc.want)
			}
		})
	}
}
