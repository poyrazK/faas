package main

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
)

type noRuntimeIdentityStore struct{ state.Store }

type missingLocalNode struct{}

func (missingLocalNode) ComputeNodeByName(context.Context, string) (state.ComputeNode, error) {
	return state.ComputeNode{}, state.ErrNotFound
}

// adr: 435 — a single-box vmmd with a store publishes its runtime identity
// under the default-local node instead of failing startup with no node ID.
func TestLocalRuntimeNodeIDResolvesSingleBoxNode(t *testing.T) {
	s := state.NewMemStore()
	local, err := s.ComputeNodeByName(t.Context(), state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	named := uuid.NewString()
	for _, tc := range []struct {
		name   string
		store  computeNodeByName
		nodeID string
		want   string
	}{
		{"named node keeps its registration", s, named, named},
		{"single box with a store", s, "", local.ID},
		{"DB-less legacy node", nil, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := localRuntimeNodeID(t.Context(), tc.store, tc.nodeID)
			if err != nil || got != tc.want {
				t.Fatalf("localRuntimeNodeID = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
	if _, err := localRuntimeNodeID(t.Context(), missingLocalNode{}, ""); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing default-local row: %v", err)
	}
	// The resolved ID yields an identity that validates and registers.
	identity := runtimeadmission.Identity{ProtocolVersion: runtimeadmission.ProtocolVersion, NodeID: local.ID, Incarnation: uuid.NewString()}
	if err := identity.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := registerRuntimeAdmissionIdentity(t.Context(), s, identity); err != nil {
		t.Fatal(err)
	}
}

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
