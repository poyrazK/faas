// adr: 20261017201008
package sched_test

import (
	"context"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
)

type legacyCommandWireNode struct {
	vmmdpb.UnimplementedVmmdServer
	boots int
}

func (n *legacyCommandWireNode) RestoreAppTask(context.Context, *vmmdpb.RestoreAppTaskRequest) (*vmmdpb.RestoreAppTaskResponse, error) {
	n.boots++
	return &vmmdpb.RestoreAppTaskResponse{}, nil
}

type commandWireNode struct {
	vmmdpb.UnimplementedVmmdServer
	mode                         string
	legacy, contained, destroyed int
}

func (n *commandWireNode) RestoreAppTask(context.Context, *vmmdpb.RestoreAppTaskRequest) (*vmmdpb.RestoreAppTaskResponse, error) {
	n.legacy++
	return &vmmdpb.RestoreAppTaskResponse{}, nil
}
func (n *commandWireNode) RestoreRehearsalAppTask(_ context.Context, r *vmmdpb.RestoreAppTaskRequest) (*vmmdpb.RestoreAppTaskResponse, error) {
	n.contained++
	out := &vmmdpb.RestoreAppTaskResponse{Instance: r.Instance, Netns: "rehearsal-command", HostIp: "10.100.1.2", CommandVersion: 1, RehearsalRunId: r.App.RehearsalRunId, SupportsSecretAliases: true}
	if n.mode == "missing-version" {
		out.CommandVersion = 0
	}
	if n.mode == "wrong-run" {
		out.RehearsalRunId = "wrong"
	}
	if n.mode == "missing-network" {
		out.Netns = ""
	}
	return out, nil
}
func (n *commandWireNode) Destroy(context.Context, *vmmdpb.DestroyRequest) (*vmmdpb.DestroyResponse, error) {
	n.destroyed++
	return &vmmdpb.DestroyResponse{}, nil
}

func TestContainedCommandClientRequiresDistinctRuntimeMethodAndAcknowledgement(t *testing.T) {
	spec := sched.AppTaskRestoreSpec{Instance: "task", DeploymentID: "deployment", App: sched.AppSpec{Plan: api.PlanPro, RehearsalRunID: "abababababababababababababababab"}}
	for _, mode := range []string{"complete", "missing-version", "wrong-run", "missing-network"} {
		t.Run(mode, func(t *testing.T) {
			node := &commandWireNode{mode: mode}
			client := rehearsalWireClient(t, node)
			out, err := client.RestoreAppTask(t.Context(), spec)
			if (err == nil) != (mode == "complete") {
				t.Fatalf("ack %s: %+v %v", mode, out, err)
			}
			if node.legacy != 0 || node.contained != 1 || mode != "complete" && node.destroyed != 1 {
				t.Fatalf("unsafe restore/fallback: %+v", node)
			}
		})
	}
	t.Run("old-runtime", func(t *testing.T) {
		node := &legacyCommandWireNode{}
		client := rehearsalWireClient(t, node)
		if _, err := client.RestoreAppTask(t.Context(), spec); err == nil || node.boots != 0 {
			t.Fatal("old runtime accepted contained command preparation")
		}
	})
}
