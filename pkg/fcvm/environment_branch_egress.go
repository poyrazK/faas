// adr: 20261031193635
package fcvm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/onebox-faas/faas/pkg/state"
)

// Contained environment branches (ADR-20261031193635 section 7, slice 6a).
// vmmd reads a wake's containment from the deployment it boots. A request
// field can only ask for containment, never waive it, so a legacy caller
// cannot boot a contained branch with live egress.

// EnvironmentBranchEgressAuthority is the durable branch egress policy.
type EnvironmentBranchEgressAuthority interface {
	EnvironmentBranchEgress(ctx context.Context, deploymentID string) (state.EnvironmentBranchEgressPolicy, error)
	EnvironmentBranchDatabaseEndpoints(ctx context.Context, deploymentID string) ([]state.EnvironmentBranchDatabaseEndpoint, error)
}

// ErrEnvironmentBranchExternalActionDenied refuses a contained branch
// instance the host-mediated channels IP policy cannot constrain: the
// outbound relay, workload identity and event publishing.
var ErrEnvironmentBranchExternalActionDenied = errors.New("a contained environment branch takes no host-mediated external action")

// environmentBranchPolicyCacheMax bounds the per-deployment policy cache.
const environmentBranchPolicyCacheMax = 4096

// environmentBranchContainment is what a contained wake carries from
// admission to the instance: the branch databases the guest may reach.
type environmentBranchContainment struct {
	databases map[string]map[uint16]bool // lower-case host -> ports
}

func (c *environmentBranchContainment) allows(name string, port uint16) bool {
	if c == nil || port == 0 {
		return false
	}
	return c.databases[strings.ToLower(strings.TrimSuffix(name, "."))][port]
}

// environmentBranchPolicyCache remembers uncontained deployments. A
// deployment's scope and its branch's egress never change, so a deployment
// once read as outside any contained branch stays so; contained and deleted
// branches are read again on every wake.
type environmentBranchPolicyCache struct {
	mu          sync.Mutex
	uncontained map[string]bool
}

func (c *environmentBranchPolicyCache) known(deploymentID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.uncontained[deploymentID]
}

func (c *environmentBranchPolicyCache) remember(deploymentID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.uncontained == nil || len(c.uncontained) >= environmentBranchPolicyCacheMax {
		c.uncontained = make(map[string]bool)
	}
	c.uncontained[deploymentID] = true
}

// WithEnvironmentBranchEgress is daemon wiring, installed before any
// admission. Without it a wake that asks for containment is refused.
func (m *Manager) WithEnvironmentBranchEgress(authority EnvironmentBranchEgressAuthority) *Manager {
	m.environmentBranchEgress = authority
	return m
}

func (m *Manager) atomicFirewall() bool {
	if m.nativeVMM() != nil {
		return true
	}
	_, ok := m.run.(InputRunner)
	return ok
}

// applyEnvironmentBranchEgress resolves the wake's branch containment.
func (m *Manager) applyEnvironmentBranchEgress(ctx context.Context, req *WakeRequest) error {
	req.environmentBranch = nil
	if m.environmentBranchEgress == nil {
		if req.EnvironmentBranchContained {
			return errors.New("environment branch containment authority is unavailable on this node")
		}
		return nil
	}
	if req.AppID == "" || req.DeploymentID == "" {
		if req.EnvironmentBranchContained {
			return errors.New("environment branch containment requires a deployment identity")
		}
		return nil
	}
	if !req.EnvironmentBranchContained && m.environmentBranchPolicies.known(req.DeploymentID) {
		return nil
	}
	policy, err := m.environmentBranchEgress.EnvironmentBranchEgress(ctx, req.DeploymentID)
	switch {
	case errors.Is(err, state.ErrEnvironmentBranchGone):
		return fmt.Errorf("wake %s: %w", req.Instance, err)
	case errors.Is(err, state.ErrNotFound) && !req.EnvironmentBranchContained:
		return nil
	case err != nil:
		return fmt.Errorf("read environment branch egress: %w", err)
	}
	if !policy.Contained() {
		if req.EnvironmentBranchContained {
			return errors.New("environment branch egress differs from the requested containment")
		}
		m.environmentBranchPolicies.remember(req.DeploymentID)
		return nil
	}
	switch {
	case req.RehearsalRunID != "":
		return errors.New("a rehearsal run cannot contain an environment branch")
	case req.ExecutionOnly || req.ExportDir != "" || req.admission != nil:
		return errors.New("environment branch containment does not support this execution mode")
	case req.StaticEgressIP != "":
		return errors.New("a contained environment branch has no static egress address")
	case !m.atomicFirewall():
		return errors.New("environment branch containment requires atomic firewall transactions")
	}
	endpoints, err := m.environmentBranchEgress.EnvironmentBranchDatabaseEndpoints(ctx, req.DeploymentID)
	if err != nil {
		return fmt.Errorf("read environment branch databases: %w", err)
	}
	containment := &environmentBranchContainment{databases: map[string]map[uint16]bool{}}
	for _, e := range endpoints {
		if e.Port < 1 || e.Port > 65535 || e.Host == "" {
			continue
		}
		host := strings.ToLower(e.Host)
		if containment.databases[host] == nil {
			containment.databases[host] = map[uint16]bool{}
		}
		containment.databases[host][uint16(e.Port)] = true
	}
	req.environmentBranch = containment
	return nil
}

// ensureEnvironmentBranchNetwork rechecks a resident contained instance's
// branch and reinstalls its profile before the guest resumes.
func (m *Manager) ensureEnvironmentBranchNetwork(ctx context.Context, inst *Instance) error {
	m.mu.Lock()
	nc, deploymentID, generation := inst.Net, inst.DeploymentID, inst.nativeGeneration
	m.mu.Unlock()
	if !nc.EnvironmentBranchContained {
		return nil
	}
	if m.environmentBranchEgress == nil {
		return errors.New("environment branch containment authority is unavailable on this node")
	}
	policy, err := m.environmentBranchEgress.EnvironmentBranchEgress(ctx, deploymentID)
	if err != nil {
		return fmt.Errorf("recheck environment branch egress: %w", err)
	}
	if !policy.Contained() {
		return errors.New("resident environment branch egress differs from its durable policy")
	}
	if !m.atomicFirewall() {
		return errors.New("environment branch containment requires atomic firewall transactions")
	}
	scope, err := m.nativeInstanceNetworkContext(ctx, inst.Lease.Instance, generation)
	if err != nil {
		return err
	}
	return m.runNftCommands(scope, nc.Netns, nc.EnvironmentBranchNftCommands())
}
