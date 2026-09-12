package main

import (
	"reflect"
	"testing"
)

func TestScheddCapabilityDeclarationMatchesConntrackRequirement(t *testing.T) {
	want := []string{"cap_net_admin"}
	if !reflect.DeepEqual(capsDecl.Allow, want) {
		t.Fatalf("allowed capabilities = %v, want %v", capsDecl.Allow, want)
	}
	if len(capsDecl.Deny) != 0 {
		t.Fatalf("unexpected denied capabilities: %v", capsDecl.Deny)
	}
}
