// adr: 435 — route fresh promotion authority without a legacy resume fallback.

package sched

import (
	"context"
	"errors"
	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"testing"
)

type promotedRouterClient struct {
	admittedRouterClient
	promotions int
}

func (c *promotedRouterClient) PromoteAdmittedRuntime(context.Context, *vmmdpb.PromoteAdmittedRuntimeRequest) (runtimeadmission.Receipt, error) {
	c.promotions++
	return runtimeadmission.Receipt{}, nil
}

func TestVMMRouterPromotionRefusesLegacyAndWrongNode(t *testing.T) {
	node := uuid.NewString()
	r := &VMMRouter{cache: map[string]VMM{node: &fakeRouterVMM{}}}
	req := &vmmdpb.PromoteAdmittedRuntimeRequest{Binding: &vmmdpb.RuntimeBootBinding{NodeId: node}}
	if _, err := r.PromoteAdmittedRuntime(t.Context(), node, req); !errors.Is(err, runtimeadmission.ErrUnavailable) {
		t.Fatalf("legacy promotion: %v", err)
	}
	c := &promotedRouterClient{}
	r.cache[node] = c
	req.Binding.NodeId = uuid.NewString()
	if _, err := r.PromoteAdmittedRuntime(t.Context(), node, req); !errors.Is(err, runtimeadmission.ErrStale) || c.promotions != 0 {
		t.Fatalf("misdirected grant: %v", err)
	}
	req.Binding.NodeId = node
	if _, err := r.PromoteAdmittedRuntime(t.Context(), node, req); err != nil || c.promotions != 1 {
		t.Fatalf("native promotion route: %v", err)
	}
}
