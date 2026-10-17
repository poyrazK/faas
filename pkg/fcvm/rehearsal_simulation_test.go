// adr: 1350
package fcvm

import (
	"context"
	"errors"
	"testing"
)

type simulationPolicyStub struct {
	network, simulation string
	err                 error
}

func (p *simulationPolicyStub) ScenarioRehearsalNetworkRun(context.Context, string) (string, error) {
	return p.network, p.err
}
func (p *simulationPolicyStub) ScenarioRehearsalSimulationRun(context.Context, string) (string, error) {
	return p.simulation, p.err
}

func TestRehearsalSimulationRelayRequiresInstalledAndDurableIdentity(t *testing.T) {
	p := &simulationPolicyStub{network: rehearsalNetworkTestRun, simulation: rehearsalNetworkTestRun}
	m := newTestManager(&fakeRunner{}, &fakeVMM{}).WithRehearsalNetworkPolicies(p)
	m.RegisterInstanceForTest("instance", "deployment", "app", "account")
	if _, _, _, err := m.InstanceRehearsalRelayIdentity(t.Context(), "instance"); !errors.Is(err, ErrRehearsalExternalActionDenied) {
		t.Fatalf("unenforced instance gained simulation: %v", err)
	}
	m.live["instance"].Net.RehearsalRunID = rehearsalNetworkTestRun
	app, account, run, err := m.InstanceRehearsalRelayIdentity(t.Context(), "instance")
	if err != nil || app != "app" || account != "account" || run != rehearsalNetworkTestRun {
		t.Fatalf("simulation identity: %q %q %q %v", app, account, run, err)
	}
	if _, _, err := m.InstanceExternalActionIdentity(t.Context(), "instance"); !errors.Is(err, ErrRehearsalExternalActionDenied) {
		t.Fatal("simulation enabled live external actions")
	}
	p.err = errors.New("offline")
	if _, _, _, err := m.InstanceRehearsalRelayIdentity(t.Context(), "instance"); err == nil {
		t.Fatal("offline policy granted simulation")
	}
}

func TestRehearsalSimulationUnownedRunsKeepTheirGrantPath(t *testing.T) {
	m := newTestManager(&fakeRunner{}, &fakeVMM{}).WithRehearsalNetworkPolicies(&simulationPolicyStub{err: errors.New("must not query an empty app identity")})
	m.RegisterInstanceForTest("execution", "deployment")
	if _, _, run, err := m.InstanceRehearsalRelayIdentity(t.Context(), "execution"); err != nil || run != "" {
		t.Fatalf("unowned execution entered app simulation policy: %q %v", run, err)
	}
}
