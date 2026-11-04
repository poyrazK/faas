// adr: 20261031193635
package sched_test

import (
	"context"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
)

type branchWireNode struct {
	vmmdpb.UnimplementedVmmdServer
	ack       bool
	requested []bool
	destroyed int
}

func (n *branchWireNode) respond(instance string, app *vmmdpb.AppSpec) *vmmdpb.WakeResponse {
	n.requested = append(n.requested, app.GetEnvironmentBranchContained())
	return &vmmdpb.WakeResponse{Instance: instance, Netns: "fc-branch", EnvironmentBranchContained: n.ack}
}

func (n *branchWireNode) CreateColdBoot(_ context.Context, r *vmmdpb.CreateColdBootRequest) (*vmmdpb.WakeResponse, error) {
	return n.respond(r.GetInstance(), r.GetApp()), nil
}

func (n *branchWireNode) CreateFromSnapshot(_ context.Context, r *vmmdpb.CreateFromSnapshotRequest) (*vmmdpb.WakeResponse, error) {
	return n.respond(r.GetInstance(), r.GetApp()), nil
}

func (n *branchWireNode) Destroy(context.Context, *vmmdpb.DestroyRequest) (*vmmdpb.DestroyResponse, error) {
	n.destroyed++
	return &vmmdpb.DestroyResponse{}, nil
}

// ADR-20261031193635 slice 6a: a contained branch boot asks for the profile,
// and a runtime that does not acknowledge it (an older vmmd ignores the
// field) is destroyed.
func TestEnvironmentBranchContainmentRequiresAcknowledgement(t *testing.T) {
	app := sched.AppSpec{Plan: api.PlanPro, EnvironmentBranchContained: true}
	for _, ack := range []bool{false, true} {
		n := &branchWireNode{ack: ack}
		c := rehearsalWireClient(t, n)
		_, coldErr := c.CreateColdBoot(t.Context(), "cold", app)
		_, restoreErr := c.CreateFromSnapshot(t.Context(), "restore", app, sched.SnapshotRef{})
		if (coldErr == nil) != ack || (restoreErr == nil) != ack {
			t.Fatalf("ack=%v: cold %v, restore %v", ack, coldErr, restoreErr)
		}
		if len(n.requested) != 2 || !n.requested[0] || !n.requested[1] {
			t.Fatalf("ack=%v: the boots did not ask for containment: %v", ack, n.requested)
		}
		if want := map[bool]int{false: 2, true: 0}[ack]; n.destroyed != want {
			t.Fatalf("ack=%v: destroyed %d runtimes, want %d", ack, n.destroyed, want)
		}
	}
	// An uncontained boot needs no acknowledgement.
	n := &branchWireNode{}
	c := rehearsalWireClient(t, n)
	if _, err := c.CreateColdBoot(t.Context(), "live", sched.AppSpec{Plan: api.PlanPro}); err != nil || n.destroyed != 0 {
		t.Fatalf("uncontained boot: %v, destroyed %d", err, n.destroyed)
	}
}
