package sched_test

// adr: 20261122132055 §7 — a session whose node starts draining is
// evacuated once; a resume of a capture only a draining node holds waits
// for its upload instead of going elsewhere.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestSandboxEvacuatedWhenItsNodeDrains(t *testing.T) {
	prev := *sched.SandboxDrainCheckEvery
	*sched.SandboxDrainCheckEvery = 10 * time.Millisecond
	t.Cleanup(func() { *sched.SandboxDrainCheckEvery = prev })
	runner, _, _, node, store := sandboxRunnerPoolStoreFixture(t, "base/sandbox-layer-v1-amd64.ext4", "agents-1")
	var mu sync.Mutex
	var reasons []string
	control := make(chan sched.SandboxControl, 1)
	hooks := sched.SandboxHooks{Evacuate: func(_ context.Context, reason string) error {
		mu.Lock()
		reasons = append(reasons, reason)
		mu.Unlock()
		return nil
	}}
	done := make(chan error, 1)
	go func() { done <- runner.RunSandbox(context.Background(), sandboxAdmission(), nil, hooks, control) }()
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	if len(reasons) != 0 {
		t.Fatalf("an active node's session evacuated: %v", reasons)
	}
	mu.Unlock()
	if err := store.NodeSetLifecycle(context.Background(), node, state.NodeLifecycleActive, state.NodeLifecycleDraining); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := len(reasons)
		mu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no evacuation from the draining node")
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	control <- sched.SandboxClose
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(reasons) != 1 || reasons[0] != sched.SandboxEvacuateNodeDraining {
		t.Fatalf("evacuations = %v, want one node_draining", reasons)
	}
}

func TestSandboxResumeWaitsForADrainingNodesUpload(t *testing.T) {
	runner, _, _, node, store := sandboxRunnerPoolStoreFixture(t, "base/sandbox-layer-v1-amd64.ext4", "agents-1", "agents-2")
	if err := store.NodeSetLifecycle(context.Background(), node, state.NodeLifecycleActive, state.NodeLifecycleDraining); err != nil {
		t.Fatal(err)
	}
	a := sandboxAdmission()
	a.Persistent = true
	a.Resume = &sched.SandboxResume{NodeID: node, RequireNode: true,
		Snapshot: sched.AgentRunSnapshot{StorageKey: sessionCapture, FCVersion: "1.10.0", MemBytes: 1}}
	err := runner.RunSandbox(context.Background(), a, nil, sched.SandboxHooks{}, nil)
	var problem *api.Problem
	if !errors.As(err, &problem) || problem.Code != api.CodeCapacity {
		t.Fatalf("resume of a draining node's node-only capture = %v, want a capacity refusal", err)
	}
}
