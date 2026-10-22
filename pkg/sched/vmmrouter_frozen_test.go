// adr: 1151 — the router forwards freeze and thaw to a node's vmmd client
// and answers 501 when that client lacks the frozen-tier capability.
package sched

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type frozenRouterVMM struct {
	fakeRouterVMM
	freezes []string
	thaws   []string
	reclaim bool
	check   bool
}

func (f *frozenRouterVMM) FreezeInstance(_ context.Context, instance string, reclaim bool) (FreezeReport, error) {
	f.freezes = append(f.freezes, instance)
	f.reclaim = reclaim
	return FreezeReport{Pause: time.Millisecond, ResidentBytesBefore: 64 << 20, ResidentBytesAfter: 64 << 20}, nil
}

func (f *frozenRouterVMM) ThawInstance(_ context.Context, instance string, req ThawRequest) (ThawResult, error) {
	f.thaws = append(f.thaws, instance)
	f.check = req.ImageHealthcheckRequired
	return ThawResult{HealthcheckVerified: req.ImageHealthcheckRequired}, nil
}

func TestVMMRouterFrozenTierUnsupportedClient(t *testing.T) {
	r := NewVMMRouter(nil, nil, nil)
	r.SetClient("node-1", &fakeRouterVMM{})
	_, freezeErr := r.FreezeInstance(t.Context(), "node-1", "inst", false)
	_, thawErr := r.ThawInstance(t.Context(), "node-1", "inst", ThawRequest{})
	for name, err := range map[string]error{"freeze": freezeErr, "thaw": thawErr} {
		var prob *api.Problem
		if !errors.As(err, &prob) || prob.Code != api.CodeNotImplemented || prob.Status != 501 {
			t.Fatalf("%s on a client without the capability = %v, want 501 %s", name, err, api.CodeNotImplemented)
		}
	}
}

func TestVMMRouterFrozenTierForwardsToNode(t *testing.T) {
	r := NewVMMRouter(nil, nil, nil)
	node := &frozenRouterVMM{}
	r.SetClient("node-1", node)
	rep, err := r.FreezeInstance(t.Context(), "node-1", "inst", true)
	if err != nil || rep.ResidentBytesAfter != 64<<20 || rep.Pause != time.Millisecond {
		t.Fatalf("FreezeInstance = %+v, %v", rep, err)
	}
	result, err := r.ThawInstance(t.Context(), "node-1", "inst", ThawRequest{ImageHealthcheckRequired: true})
	if err != nil || !result.HealthcheckVerified {
		t.Fatalf("ThawInstance = %+v, %v", result, err)
	}
	if len(node.freezes) != 1 || len(node.thaws) != 1 || !node.reclaim || !node.check {
		t.Fatalf("node saw freezes=%v thaws=%v reclaim=%v check=%v", node.freezes, node.thaws, node.reclaim, node.check)
	}
	var prob *api.Problem
	if _, err := r.FreezeInstance(t.Context(), "unknown", "inst", false); !errors.As(err, &prob) || prob.Code != api.CodeCapacity {
		t.Fatalf("FreezeInstance on an unknown node = %v, want %s", err, api.CodeCapacity)
	}
}
