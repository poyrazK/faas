// adr: 20261027090013
package sched_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// sandboxRouter holds the session until close and checks the reservation
// while it lives.
type sandboxRouter struct {
	*agentRouter
	spec     sched.SandboxSpec
	resident bool
}

func (r *sandboxRouter) RunSandbox(ctx context.Context, nodeID string, spec sched.SandboxSpec, hooks sched.SandboxHooks, control <-chan sched.SandboxControl) error {
	r.nodeID, r.spec, r.resident = nodeID, spec, r.ledger.ResidentFor(spec.Instance)
	if hooks.Booted != nil {
		if err := hooks.Booted(ctx, "WAKE_COLD_BOOT", 10); err != nil {
			return err
		}
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case req := <-control:
			switch req {
			case sched.SandboxClose:
				return nil
			case sched.SandboxFreeze:
				if hooks.Frozen != nil {
					_ = hooks.Frozen(ctx)
				}
			}
		}
	}
}

func sandboxRunnerFixture(t *testing.T, layer string) (*sched.AgentRunner, *sandboxRouter, *sched.NodeLedger, string) {
	t.Helper()
	ctx := context.Background()
	store := state.NewMemStore()
	var agentNode string
	for _, name := range []string{"compute-1", "agents-1"} {
		n, err := store.UpsertComputeNode(ctx, state.ComputeNode{Name: name, TargetURL: "tcp://" + name + ":50051",
			VPCPUs: 20, MemMB: 64000, MaxConcurrency: 200, AdmissionCeilingMB: 47600, VCPUBudget: 40, Active: true})
		if err != nil {
			t.Fatal(err)
		}
		agentNode = n.ID
	}
	ledger := sched.NewNodeLedger()
	engine, err := sched.NewEngine(ctx, store, ledger, nil, nil, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	router := &sandboxRouter{agentRouter: &agentRouter{ledger: ledger}}
	runner, err := sched.NewAgentRunner(engine, router, sched.AgentRunConfig{BaseKey: "base/agent-base.ext4",
		LayerKey: "layer/agent.ext4", SandboxLayerKey: layer, Nodes: []string{"agents-1"}})
	if err != nil {
		t.Fatal(err)
	}
	return runner, router, ledger, agentNode
}

func sandboxAdmission() sched.SandboxAdmission {
	return sched.SandboxAdmission{SandboxID: "11111111-2222-3333-4444-555555555555", AccountID: "acct", Plan: api.PlanPro,
		RAMMB: 1024, VCPU: 1, CPUMillicores: 1000, Network: api.SandboxNetworkRegistries}
}

func TestSandboxRunnerHoldsTheReservationUntilClose(t *testing.T) {
	runner, router, ledger, node := sandboxRunnerFixture(t, "base/sandbox-layer-v1-amd64.ext4")
	if !runner.SandboxesEnabled() {
		t.Fatal("sessions off with a sandbox layer")
	}
	control := make(chan sched.SandboxControl, 1)
	frozen := make(chan struct{}, 1)
	var placement sched.AgentRunPlacement
	done := make(chan error, 1)
	go func() {
		done <- runner.RunSandbox(context.Background(), sandboxAdmission(), func(p sched.AgentRunPlacement) error {
			placement = p
			return nil
		}, sched.SandboxHooks{Frozen: func(context.Context) error { frozen <- struct{}{}; return nil }}, control)
	}()
	control <- sched.SandboxFreeze
	select {
	case <-frozen:
	case <-time.After(5 * time.Second):
		t.Fatal("freeze never reached the router")
	}
	if !ledger.ResidentFor(sched.SandboxInstanceID(sandboxAdmission().SandboxID)) {
		t.Fatal("the reservation was released while the session lives")
	}
	control <- sched.SandboxClose
	if err := <-done; err != nil {
		t.Fatalf("close: %v", err)
	}
	if placement.NodeID != node || placement.Instance != "sandbox-11111111-2222-3333-4444-555555555555" || router.nodeID != node || !router.resident {
		t.Fatalf("placement %+v, router node %s resident %v", placement, router.nodeID, router.resident)
	}
	if s := router.spec; s.LayerKey != "base/sandbox-layer-v1-amd64.ext4" || s.BaseKey != "base/agent-base.ext4" || s.SandboxID != sandboxAdmission().SandboxID ||
		s.MemSizeMiB != 1024 || s.QuietPauseAfter != api.QuietPauseAfterMillis*time.Millisecond || s.Network != api.SandboxNetworkRegistries {
		t.Fatalf("spec = %+v", s)
	}
	if ledger.ResidentFor(placement.Instance) {
		t.Fatal("the reservation outlived the session")
	}
}

func TestSandboxRunnerRefusals(t *testing.T) {
	runner, _, ledger, _ := sandboxRunnerFixture(t, "")
	if runner.SandboxesEnabled() {
		t.Fatal("sessions on without a sandbox layer")
	}
	if err := runner.RunSandbox(context.Background(), sandboxAdmission(), nil, sched.SandboxHooks{}, nil); !errors.Is(err, sched.ErrAgentRunsDisabled) {
		t.Fatalf("no layer: %v", err)
	}
	runner, _, ledger, _ = sandboxRunnerFixture(t, "base/sandbox-layer-v1-amd64.ext4")
	for name, edit := range map[string]func(*sched.SandboxAdmission){
		"free plan": func(a *sched.SandboxAdmission) { a.Plan = api.PlanFree },
		"odd RAM":   func(a *sched.SandboxAdmission) { a.RAMMB = 1000 },
		"no vCPU":   func(a *sched.SandboxAdmission) { a.VCPU = 0 },
		"web":       func(a *sched.SandboxAdmission) { a.Network = api.SandboxNetworkWeb },
	} {
		a := sandboxAdmission()
		edit(&a)
		var invalid *sched.AgentRunInvalidError
		if err := runner.RunSandbox(context.Background(), a, nil, sched.SandboxHooks{}, nil); !errors.As(err, &invalid) {
			t.Errorf("%s: %v, want AgentRunInvalidError", name, err)
		}
	}
	placedErr := errors.New("agentd lost the session")
	err := runner.RunSandbox(context.Background(), sandboxAdmission(), func(sched.AgentRunPlacement) error { return placedErr }, sched.SandboxHooks{}, nil)
	if !errors.Is(err, placedErr) || ledger.ResidentFor(sched.SandboxInstanceID(sandboxAdmission().SandboxID)) {
		t.Fatalf("placed refusal: %v; the reservation must be released", err)
	}
}
