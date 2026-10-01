// adr: 386 — route managed native grants to their exact configured node.

package sched

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

type admittedRouterClient struct {
	fakeRouterVMM
	identity      runtimeadmission.Identity
	admittedCalls int
}

func (c *admittedRouterClient) RuntimeAdmissionIdentity(context.Context) (runtimeadmission.Identity, error) {
	return c.identity, nil
}
func (c *admittedRouterClient) CreateAdmittedRuntime(context.Context, *vmmdpb.CreateAdmittedRuntimeRequest) (*WakeOutcome, error) {
	c.admittedCalls++
	return &WakeOutcome{}, nil
}

func TestVMMRouterAdmittedRuntimeRefusesUnsupportedAndMisdirectedNodes(t *testing.T) {
	node := uuid.NewString()
	legacy := &fakeRouterVMM{}
	router := &VMMRouter{cache: map[string]VMM{node: legacy}}
	req := &vmmdpb.CreateAdmittedRuntimeRequest{Binding: &vmmdpb.RuntimeBootBinding{NodeId: node}}
	if _, err := router.RuntimeAdmissionIdentity(t.Context(), node); !errors.Is(err, runtimeadmission.ErrUnavailable) {
		t.Fatalf("legacy identity err=%v", err)
	}
	if _, err := router.CreateAdmittedRuntime(t.Context(), node, req); !errors.Is(err, runtimeadmission.ErrUnavailable) {
		t.Fatalf("legacy boot err=%v", err)
	}
	if len(legacy.instanceCalls) != 0 {
		t.Fatal("router fell back to legacy wake")
	}
	client := &admittedRouterClient{identity: runtimeadmission.Identity{ProtocolVersion: runtimeadmission.ProtocolVersion, NodeID: uuid.NewString(), Incarnation: uuid.NewString()}}
	router.cache[node] = client
	if _, err := router.RuntimeAdmissionIdentity(t.Context(), node); !errors.Is(err, runtimeadmission.ErrStale) {
		t.Fatal("wrong-node probe accepted")
	}
	req.Binding.NodeId = client.identity.NodeID
	if _, err := router.CreateAdmittedRuntime(t.Context(), node, req); !errors.Is(err, runtimeadmission.ErrStale) || client.admittedCalls != 0 {
		t.Fatal("grant sent to wrong node")
	}
	client.identity.NodeID = node
	req.Binding.NodeId = node
	if _, err := router.RuntimeAdmissionIdentity(t.Context(), node); err != nil {
		t.Fatal(err)
	}
	if _, err := router.CreateAdmittedRuntime(t.Context(), node, req); err != nil || client.admittedCalls != 1 {
		t.Fatal("valid native capability not routed")
	}
}
