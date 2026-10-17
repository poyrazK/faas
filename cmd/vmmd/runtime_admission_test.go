package main

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
)

type noRuntimeIdentityStore struct{ state.Store }

func TestRegisterRuntimeAdmissionIdentityRefusesMissingRegistrar(t *testing.T) {
	if err := registerRuntimeAdmissionIdentity(t.Context(), noRuntimeIdentityStore{}, runtimeadmission.Identity{}); err == nil {
		t.Fatal("vmmd accepted an unregistered native process")
	}
}

func TestRegisterRuntimeAdmissionIdentityValidatesOwnedNode(t *testing.T) {
	s := state.NewMemStore()
	node, err := s.ComputeNodeByName(t.Context(), state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	identity := runtimeadmission.Identity{ProtocolVersion: runtimeadmission.ProtocolVersion, NodeID: node.ID, Incarnation: uuid.NewString()}
	if err := registerRuntimeAdmissionIdentity(t.Context(), s, identity); err != nil {
		t.Fatal(err)
	}
	identity.NodeID = uuid.NewString()
	if err := registerRuntimeAdmissionIdentity(t.Context(), s, identity); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("native process registered another node: %v", err)
	}
	identity.NodeID = node.ID
	identity.Incarnation = ""
	if err := registerRuntimeAdmissionIdentity(t.Context(), s, identity); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("native process omitted its incarnation: %v", err)
	}
}

// The single-box vmmd (no node name, with a store) publishes its identity for
// the default-local node; a named node keeps its own ID. Without this, every
// single-box vmmd refused to start after ADR-435's startup registration.
func TestRuntimeAdmissionNodeIDResolvesDefaultLocal(t *testing.T) {
	s := state.NewMemStore()
	local, err := s.ComputeNodeByName(t.Context(), state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := runtimeAdmissionNodeID(t.Context(), s, ""); err != nil || got != local.ID {
		t.Fatalf("single-box node %q %v, want %s", got, err, local.ID)
	}
	named := uuid.NewString()
	if got, err := runtimeAdmissionNodeID(t.Context(), s, named); err != nil || got != named {
		t.Fatalf("named node %q %v", got, err)
	}
	if got, err := runtimeAdmissionNodeID(t.Context(), nil, ""); err != nil || got != "" {
		t.Fatalf("storeless vmmd %q %v", got, err)
	}
	identity := runtimeadmission.Identity{ProtocolVersion: runtimeadmission.ProtocolVersion, NodeID: local.ID, Incarnation: uuid.NewString()}
	if err := registerRuntimeAdmissionIdentity(t.Context(), s, identity); err != nil {
		t.Fatalf("default-local identity: %v", err)
	}
}
