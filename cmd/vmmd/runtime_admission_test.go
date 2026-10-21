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
