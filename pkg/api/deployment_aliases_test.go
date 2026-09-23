package api

import (
	"strings"
	"testing"
)

func TestValidDeploymentAliasName(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{name: "candidate", want: true},
		{name: "qa-2", want: true},
		{name: "x", want: true},
		{name: strings.Repeat("a", 63), want: true},
		{name: strings.Repeat("a", 64), want: false},
		{name: "Upper", want: false},
		{name: "-candidate", want: false},
		{name: "candidate-", want: false},
		{name: "has.dot", want: false},
		{name: "has_under", want: false},
		{name: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ValidDeploymentAliasName(tt.name); got != tt.want {
				t.Fatalf("ValidDeploymentAliasName(%q) = %t, want %t", tt.name, got, tt.want)
			}
		})
	}
}
