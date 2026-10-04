package main

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRegisterRuntimeAdmissionIdentityFencesSupersededStartup(t *testing.T) {
	s := state.NewMemStore()
	ctx := t.Context()
	node, err := s.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	old := runtimeadmission.Identity{NodeID: node.ID, Incarnation: uuid.NewString(), ProtocolVersion: runtimeadmission.ProtocolVersion}
	if err := registerRuntimeAdmissionIdentity(ctx, s, old); err != nil {
		t.Fatal(err)
	}
	newer := runtimeadmission.Identity{NodeID: node.ID, Incarnation: uuid.NewString(), ProtocolVersion: runtimeadmission.ArtifactProtocolVersion}
	if err := registerRuntimeAdmissionIdentity(ctx, s, newer); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := registerRuntimeAdmissionIdentity(ctx, s, old); !errors.Is(err, state.ErrApplicationStandardRuntimeStale) {
			t.Fatalf("startup wrapper hid stale process registration: %v", err)
		}
		if err := registerRuntimeAdmissionIdentity(ctx, s, newer); err != nil {
			t.Fatalf("current startup retry failed: %v", err)
		}
	}
	changed := newer
	changed.ProtocolVersion = runtimeadmission.ProtocolVersion
	if err := registerRuntimeAdmissionIdentity(ctx, s, changed); !errors.Is(err, state.ErrApplicationStandardRuntimeStale) {
		t.Fatalf("startup wrapper allowed protocol mutation: %v", err)
	}
}
