package edgevalidate

import "testing"

// TestFieldErrorReason covers the schema-keyword → bounded-reason
// mapping used by the gateway-side metric (issue #975 #3 / Mega-
// Foundation #979-a). The mapping is intentionally narrow: only the
// schema-side keyword (FieldError.Expected) is trusted, not the
// library's localized error string (FieldError.Got). New keywords
// require a paired change here + in pkg/gateway/edge_rules.go's
// EdgeValidateFieldError.Reason().

func TestFieldErrorReason(t *testing.T) {
	cases := []struct {
		name     string
		expected string
		want     string
	}{
		{"required", "required", "required_missing"},
		{"type", "type", "type_mismatch"},
		{"additionalProperties", "additionalProperties", "additional_properties_not_allowed"},
		{"enum", "enum", "enum_violation"},
		{"format", "format", "format_violation"},
		{"minimum", "minimum", "other"},
		{"maximum", "maximum", "other"},
		{"pattern", "pattern", "other"},
		{"unknown-empty", "", "other"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fe := &FieldError{Expected: tc.expected}
			if got := fe.Reason(); got != tc.want {
				t.Errorf("FieldError{Expected=%q}.Reason() = %q; want %q",
					tc.expected, got, tc.want)
			}
		})
	}
}

func TestFieldErrorReason_NilSafe(t *testing.T) {
	var fe *FieldError
	if got := fe.Reason(); got != "other" {
		t.Errorf("nil-receiver Reason() = %q; want %q", got, "other")
	}
}

// TestValidateReasonClosedSetMembership ensures the public closed set
// is non-empty and contains every value the gateway-side metric
// rejects. Drift between the two is a silent label-set bug.
func TestValidateReasonClosedSetMembership(t *testing.T) {
	want := []string{
		"required_missing",
		"type_mismatch",
		"additional_properties_not_allowed",
		"enum_violation",
		"format_violation",
		"other",
	}
	for _, k := range want {
		if _, ok := ValidateReasonClosedSet[k]; !ok {
			t.Errorf("ValidateReasonClosedSet missing %q", k)
		}
	}
	if len(ValidateReasonClosedSet) != len(want) {
		t.Errorf("ValidateReasonClosedSet size = %d; want %d (no extras allowed)",
			len(ValidateReasonClosedSet), len(want))
	}
}
