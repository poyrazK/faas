package gateway

import "testing"

// TestEdgeValidateFieldErrorReason mirrors the edgevalidate-side
// FieldError.Reason mapping. The two implementations must stay in
// lockstep — the gateway-side wrapper duplicates the logic so it
// doesn't have to import the validator backend. Drift is a silent
// label-set bug (issue #975 #3 / Mega-Foundation #979-a).

func TestEdgeValidateFieldErrorReason(t *testing.T) {
	cases := []struct {
		expected string
		want     string
	}{
		{"required", "required_missing"},
		{"type", "type_mismatch"},
		{"additionalProperties", "additional_properties_not_allowed"},
		{"enum", "enum_violation"},
		{"format", "format_violation"},
		{"minimum", "other"},
		{"maximum", "other"},
		{"pattern", "other"},
		{"", "other"},
	}
	for _, tc := range cases {
		fe := &EdgeValidateFieldError{Expected: tc.expected}
		if got := fe.Reason(); got != tc.want {
			t.Errorf("EdgeValidateFieldError{Expected=%q}.Reason() = %q; want %q",
				tc.expected, got, tc.want)
		}
	}
}

func TestEdgeValidateFieldErrorReason_NilSafe(t *testing.T) {
	var fe *EdgeValidateFieldError
	if got := fe.Reason(); got != "other" {
		t.Errorf("nil-receiver Reason() = %q; want %q", got, "other")
	}
}
