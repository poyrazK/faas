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
func TestDeploymentAliasHostLabel(t *testing.T) {
	const appID = "550e8400-e29b-41d4-a716-446655440000"
	got, ok := DeploymentAliasHostLabel(appID, "canary")
	if !ok || got != "tag-canary-550e8400e29b41d4a716446655440000" {
		t.Fatalf("DeploymentAliasHostLabel = %q, %v", got, ok)
	}
	if _, ok := DeploymentAliasHostLabel(appID, strings.Repeat("a", 27)); ok {
		t.Fatal("hostname label exceeding 63 characters was accepted")
	}
	if _, ok := DeploymentAliasHostLabel("not-an-app-id", "canary"); ok {
		t.Fatal("invalid app id accepted")
	}
}
