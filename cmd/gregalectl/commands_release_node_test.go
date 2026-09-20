package main

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/roleTemplating"
)

func TestCanonicalComputeNodeName(t *testing.T) {
	tests := []struct {
		name string
		role roleTemplating.Role
		want string
	}{
		{name: "fsn-2", role: roleTemplating.RoleComputeOnly, want: "fsn-2.faas"},
		{name: "fsn-2.faas", role: roleTemplating.RoleComputeOnly, want: "fsn-2.faas"},
		{name: "faas-control-plane", role: roleTemplating.RoleControlPlane, want: "faas-control-plane"},
		{name: "default-local", role: "", want: "default-local"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := canonicalComputeNodeName(tt.name, tt.role); got != tt.want {
				t.Fatalf("canonicalComputeNodeName(%q, %q) = %q, want %q", tt.name, tt.role, got, tt.want)
			}
		})
	}
}

func TestDeferFirstBootServiceStart(t *testing.T) {
	tests := []struct {
		name              string
		current           roleTemplating.Role
		target            roleTemplating.Role
		deferActivation   bool
		wantDeferredStart bool
	}{
		{
			name:              "new compute node stays stopped for join readiness",
			target:            roleTemplating.RoleComputeOnly,
			deferActivation:   true,
			wantDeferredStart: true,
		},
		{
			name:            "ordinary first boot preserves immediate start",
			target:          roleTemplating.RoleComputeOnly,
			deferActivation: false,
		},
		{
			name:              "existing compute rollout is managed by normal mutation",
			current:           roleTemplating.RoleComputeOnly,
			target:            roleTemplating.RoleComputeOnly,
			deferActivation:   true,
			wantDeferredStart: false,
		},
		{
			name:              "control plane never uses compute activation deferral",
			target:            roleTemplating.RoleControlPlane,
			deferActivation:   true,
			wantDeferredStart: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := deferFirstBootServiceStart(tt.current, tt.target, tt.deferActivation); got != tt.wantDeferredStart {
				t.Fatalf("deferFirstBootServiceStart(%q, %q, %t) = %t, want %t", tt.current, tt.target, tt.deferActivation, got, tt.wantDeferredStart)
			}
		})
	}
}
