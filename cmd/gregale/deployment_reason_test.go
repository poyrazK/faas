package main

import (
	"strings"
	"testing"
)

func TestValidateDeploymentReasonCountsCharacters(t *testing.T) {
	tests := []struct {
		name   string
		reason string
		valid  bool
	}{
		{"ascii boundary", strings.Repeat("a", 280), true},
		{"two byte boundary", strings.Repeat("é", 280), true},
		{"cjk boundary", strings.Repeat("界", 280), true},
		{"emoji boundary", strings.Repeat("🚀", 280), true},
		{"mixed boundary", strings.Repeat("aé界🚀", 70), true},
		{"ascii over", strings.Repeat("a", 281), false},
		{"emoji over", strings.Repeat("🚀", 281), false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateDeploymentReason(test.reason)
			if (err == nil) != test.valid {
				t.Fatalf("error = %v, valid = %v", err, test.valid)
			}
			if !test.valid && !strings.Contains(err.Error(), "got 281") {
				t.Fatalf("error = %q, want character count 281", err)
			}
		})
	}
}
