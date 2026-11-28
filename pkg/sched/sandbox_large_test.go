package sched_test

// adr: 20261122132055 §5 — a session above the largest ephemeral shape runs
// only on the agent-pool nodes that declare large shapes; with none it is
// refused for capacity. A session within it places on any pool node.

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
)

func runSandboxOnce(t *testing.T, runner *sched.AgentRunner, a sched.SandboxAdmission) error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	booted := func(context.Context, sched.SandboxBooted) error { cancel(); return nil }
	err := runner.RunSandbox(ctx, a, nil, sched.SandboxHooks{Booted: booted}, nil)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func largeSandbox() sched.SandboxAdmission {
	a := sandboxAdmission()
	a.Plan, a.Persistent, a.RAMMB, a.VCPU, a.CPUMillicores = api.PlanScale, true, 16_384, 8, 8000
	return a
}

func TestLargeSandboxRunsOnlyOnDeclaredNodes(t *testing.T) {
	runner, router, _, _, store := sandboxRunnerLargeFixture(t, "base/sandbox-layer-v1-amd64.ext4", []string{"agents-2"}, "agents-1", "agents-2")
	nodeName := func(id string) string {
		n, err := store.ComputeNodeByID(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		return n.Name
	}
	for range 3 {
		if err := runSandboxOnce(t, runner, largeSandbox()); err != nil {
			t.Fatal(err)
		}
		if n := nodeName(router.nodeID); n != "agents-2" {
			t.Fatalf("a large session placed on %q, want agents-2", n)
		}
	}
	small := sandboxAdmission()
	small.Persistent = true
	if err := runSandboxOnce(t, runner, small); err != nil {
		t.Fatalf("a small session: %v", err)
	}
}

func TestLargeSandboxWithoutDeclaredNodesIsRefusedForCapacity(t *testing.T) {
	runner, _, ledger, _, _ := sandboxRunnerLargeFixture(t, "base/sandbox-layer-v1-amd64.ext4", nil, "agents-1")
	a := largeSandbox()
	err := runner.RunSandbox(context.Background(), a, nil, sched.SandboxHooks{}, nil)
	var problem *api.Problem
	if !errors.As(err, &problem) || problem.Code != api.CodeCapacity {
		t.Fatalf("a large session with no large node = %v, want a capacity refusal", err)
	}
	if ledger.ResidentFor(sched.SandboxInstanceID(a.SandboxID)) {
		t.Fatal("a refused large session kept a reservation")
	}
}
