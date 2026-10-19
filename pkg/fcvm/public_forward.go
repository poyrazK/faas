package fcvm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"

	"github.com/onebox-faas/faas/pkg/netns"
)

// Host observations are separate from network effects so portable ownership
// fixtures can model both without inspecting the runner's host network.
type publicForwardHostProbe struct {
	checkRouting    func() error
	interfaceByName func(string) (*net.Interface, error)
}

// Capture while Wake owns this network, before publishing/prewarming a bridge.
// Test/alternate VMMs retain their namespace transport; native owners must have
// a real public interface and never fall back after a capture failure.
func (m *Manager) capturePublicForwardTarget(ctx context.Context, nc netns.Config) (*netns.PublicForwardTarget, error) {
	if m.nativeVMM() == nil {
		return nil, nil
	}
	if !nc.PublicForwarding {
		return nil, errors.New("public forward has no generation-owned host route")
	}
	checkRouting := netns.CheckPublicForwardRouting
	if m.publicForwardProbe.checkRouting != nil {
		checkRouting = m.publicForwardProbe.checkRouting
	}
	if err := checkRouting(); err != nil {
		return nil, err
	}
	interfaceByName := net.InterfaceByName
	if m.publicForwardProbe.interfaceByName != nil {
		interfaceByName = m.publicForwardProbe.interfaceByName
	}
	iface, err := interfaceByName(nc.VethHost)
	if err != nil {
		return nil, fmt.Errorf("public forward interface: %w", err)
	}
	if err := m.setupPublicForwardNeighbor(ctx, nc); err != nil {
		return nil, err
	}
	port := nc.GuestAppPort
	if port == 0 {
		port = netns.AppPort
	}
	target := &netns.PublicForwardTarget{HostIP: nc.HostIP, BridgeIP: nc.HostBridgeIP, Veth: nc.VethHost, IfIndex: iface.Index, AppPort: port}
	return target, target.Validate()
}

// Bridge ARP is delivered on its master. A route on the individual slave
// needs an explicit neighbor from the actual owned peer's MAC. Both route
// and neighbor disappear with the device. Native helper fencing joins reuse.
func (m *Manager) setupPublicForwardNeighbor(ctx context.Context, nc netns.Config) error {
	ctx, err := m.nativeNetworkContext(ctx, nc.Instance, "", nativeHostHelperEffect)
	if err != nil {
		return err
	}
	runner, err := m.networkCaptureRunner(ctx)
	if err != nil {
		return err
	}
	raw, err := runner.RunCapture(ctx, []string{"ip", "-n", nc.Netns, "-j", "link", "show", "dev", nc.VethPeer})
	if err != nil {
		return fmt.Errorf("public forward peer identity: %w", err)
	}
	var links []struct {
		Address string `json:"address"`
	}
	if err := json.Unmarshal(raw, &links); err != nil {
		return fmt.Errorf("public forward peer identity: %w", err)
	}
	if len(links) != 1 {
		return errors.New("public forward peer identity is ambiguous")
	}
	mac, err := net.ParseMAC(links[0].Address)
	if err != nil || len(mac) != 6 || mac[0]&1 != 0 {
		return errors.New("public forward peer has no unicast Ethernet identity")
	}
	if err := m.runNetworkCommand(ctx, []string{"ip", "neigh", "replace", nc.HostIP.String(), "lladdr", mac.String(), "nud", "permanent", "dev", nc.VethHost}); err != nil {
		return fmt.Errorf("public forward original-device neighbor: %w", err)
	}
	return nil
}

func (m *Manager) retainPublicForwardHostPolicy() {
	m.hostPolicyMu.Lock()
	defer m.hostPolicyMu.Unlock()
	if current := netns.ActiveHostPolicyForRender(); current != nil && !current.PublicForwarding {
		next := *current
		next.PublicForwarding = true
		netns.SwapActiveHostPolicy(next)
	}
}

// PublicForwardTarget returns a COPY of the captured generation. A missing
// native target is an error, including a networkless or retired instance.
func (m *Manager) PublicForwardTarget(instance string) (*netns.PublicForwardTarget, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	inst := m.live[instance]
	if inst == nil {
		return nil, errors.New("public forward instance is not live")
	}
	if inst.publicForwardTarget == nil {
		if m.nativeVMM() != nil {
			return nil, errors.New("public forward instance has no captured network")
		}
		return nil, nil
	}
	target := *inst.publicForwardTarget
	return &target, nil
}
