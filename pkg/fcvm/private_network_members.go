package fcvm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/state"
	"net/netip"
)

type PrivateNetworkMemberIdentity = state.PrivateNetworkMemberIdentity

type PrivateNetworkMemberRequest struct {
	AccountID, AppID, NetworkID, InstanceID, DeploymentID string
	ExpectedService                                       netip.Addr
	RequireReady, Eligible                                bool
}

func (m *Manager) WithPrivateNetworkMembers(source func(context.Context, PrivateNetworkMemberRequest) (PrivateNetworkMemberIdentity, error), release func(context.Context, string, string, string, string, bool) error) *Manager {
	m.privateNetworkMemberSource = source
	m.privateNetworkMemberRelease = release
	return m
}

// The guard also fails closed in standalone managers without a durable source.
// It includes boots that have not yet entered the live map.
func (m *Manager) reservePrivateNetworkAddresses(nc netns.Config, instance string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.privateNetworkAddressOwners == nil {
		m.privateNetworkAddressOwners = make(map[string]string)
	}
	for _, address := range []netip.Addr{nc.PrivateNetworkAddress, nc.PrivateNetworkServiceAddress} {
		if !address.IsValid() {
			continue
		}
		key := nc.PrivateNetworkBridge + "\x00" + address.String()
		if owner := m.privateNetworkAddressOwners[key]; owner != "" && owner != instance {
			return fmt.Errorf("private address %s already owned by instance %s", address, owner)
		}
	}
	for _, address := range []netip.Addr{nc.PrivateNetworkAddress, nc.PrivateNetworkServiceAddress} {
		if address.IsValid() {
			m.privateNetworkAddressOwners[nc.PrivateNetworkBridge+"\x00"+address.String()] = instance
		}
	}
	return nil
}

func (m *Manager) releasePrivateNetworkMember(ctx context.Context, nc netns.Config, instance string, serviceOnly bool) error {
	if nc.PrivateNetworkID != "" && m.privateNetworkMemberRelease != nil {
		if err := m.privateNetworkMemberRelease(ctx, nc.PrivateNetworkAccountID, nc.PrivateNetworkAppID, nc.PrivateNetworkID, instance, serviceOnly); err != nil {
			return fmt.Errorf("release private member: %w", err)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, owner := range m.privateNetworkAddressOwners {
		if owner == instance && (!serviceOnly || key == nc.PrivateNetworkBridge+"\x00"+nc.PrivateNetworkServiceAddress.String()) {
			delete(m.privateNetworkAddressOwners, key)
		}
	}
	return nil
}

// Withdraw DNAT before removing the address: a cached neighbor entry can still
// deliver packets for an unbound IP to the old namespace's prerouting hook.
func (m *Manager) changePrivateNetworkService(ctx context.Context, inst *Instance, nc netns.Config, service netip.Addr) error {
	old := nc
	nc.PrivateNetworkServiceAddress = service
	if err := m.replaceNetworkPolicy(ctx, inst, nc); err != nil {
		return err
	}
	if old.PrivateNetworkServiceAddress.IsValid() && old.PrivateNetworkServiceAddress != service {
		argv := []string{"ip", "netns", "exec", nc.Netns, "ip", "addr", "del", old.PrivateNetworkServiceAddress.String() + "/32", "dev", nc.PrivateVethPeer}
		if err := m.runNetworkCommand(ctx, argv); err != nil {
			capture, captureErr := m.networkCaptureRunner(ctx)
			if captureErr != nil || capture == nil {
				return err
			}
			output, probeErr := capture.RunCapture(ctx, []string{"ip", "netns", "exec", nc.Netns, "ip", "-j", "-4", "addr", "show", "dev", nc.PrivateVethPeer})
			var links []struct {
				Addresses []struct {
					Local string `json:"local"`
				} `json:"addr_info"`
			}
			if probeErr != nil || json.Unmarshal(output, &links) != nil || len(links) != 1 {
				return err
			}
			for _, address := range links[0].Addresses {
				if address.Local == old.PrivateNetworkServiceAddress.String() {
					return err
				}
			}
		}
		if err := m.releasePrivateNetworkMember(ctx, old, inst.Lease.Instance, true); err != nil {
			return err
		}
	}
	if service.IsValid() {
		if !nc.PrivateNetworkCIDRs[0].Contains(service) {
			return errors.New("private service address outside member network")
		}
		if err := m.reservePrivateNetworkAddresses(nc, inst.Lease.Instance); err != nil {
			return err
		}
		if err := m.runNetworkCommand(ctx, []string{"ip", "netns", "exec", nc.Netns, "ip", "addr", "replace", service.String() + "/32", "dev", nc.PrivateVethPeer}); err != nil {
			return err
		}
		if err := m.runCommands(ctx, nc.PrivateNetworkAnnounceCommands(newResourceLinkAddress())); err != nil {
			return fmt.Errorf("announce private service address: %w", err)
		}
	}
	m.mu.Lock()
	if m.live[inst.Lease.Instance] == inst {
		inst.Net.PrivateNetworkServiceAddress = service
		m.markAdoptionDirty(inst.Lease.Instance)
	}
	m.mu.Unlock()
	return nil
}

// Promotion never expires an old owner's claim. Only confirmed namespace
// cleanup or explicit service withdrawal makes the stable address available.
func (m *Manager) ReconcilePrivateNetworkServices(ctx context.Context) error {
	if m.privateNetworkMemberSource == nil {
		return nil
	}
	m.mu.Lock()
	apps := make(map[string]bool)
	for _, inst := range m.live {
		if inst.Net.PrivateNetworkID != "" {
			apps[inst.AppID] = true
		}
	}
	m.mu.Unlock()
	var failures []error
	for appID := range apps {
		err := func() error {
			unlock, err := m.lockAppEgressPolicy(ctx, appID)
			if err != nil {
				return err
			}
			defer unlock()
			m.mu.Lock()
			var targets []*Instance
			for _, inst := range m.live {
				if inst.AppID == appID && inst.Net.PrivateNetworkID != "" {
					targets = append(targets, inst)
				}
			}
			m.mu.Unlock()
			for _, inst := range targets {
				if err := m.reconcilePrivateNetworkService(ctx, inst); err != nil {
					return err
				}
			}

			return nil
		}()
		if err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (m *Manager) releaseAllPrivateNetworkMembers(ctx context.Context, nc netns.Config, instance string) error {
	if m.privateNetworkMemberRelease != nil {
		if err := m.privateNetworkMemberRelease(ctx, nc.PrivateNetworkAccountID, nc.PrivateNetworkAppID, "", instance, false); err != nil {
			return fmt.Errorf("release private members after teardown: %w", err)
		}
	}
	// Empty network avoids a second release while clearing local reservations.
	nc.PrivateNetworkID = ""
	return m.releasePrivateNetworkMember(ctx, nc, instance, false)
}

func (m *Manager) reconcilePrivateNetworkService(ctx context.Context, expected *Instance) error {
	operationCtx, inst, flight, err := m.registerLiveInstanceFlight(ctx, expected.Lease.Instance)
	if err != nil {
		return err
	}
	defer m.finishInstanceFlight(expected.Lease.Instance, flight)
	if inst != expected {
		return errors.New("private service target changed")
	}
	m.mu.Lock()
	nc, generation := inst.Net, inst.nativeGeneration
	request := PrivateNetworkMemberRequest{AccountID: inst.AccountID, AppID: inst.AppID, NetworkID: nc.PrivateNetworkID, InstanceID: inst.Lease.Instance, DeploymentID: inst.DeploymentID, Eligible: !inst.QualificationOnly && !inst.StaticEgressExcluded && !nc.Quarantine, RequireReady: true}
	m.mu.Unlock()
	identity, err := m.privateNetworkMemberSource(operationCtx, request)
	if err != nil {
		return err
	}
	if identity.Address != nc.PrivateNetworkAddress {
		return errors.New("private instance address changed")
	}
	// A failed announcement may have bound the VIP without publishing it in
	// the cached/adopted config. The durable claim supplies its cleanup identity.
	if !identity.Service.IsValid() && !nc.PrivateNetworkServiceAddress.IsValid() && identity.WithdrawService.IsValid() {
		nc.PrivateNetworkServiceAddress = identity.WithdrawService
	}
	if identity.Service == nc.PrivateNetworkServiceAddress {
		return nil
	}
	networkCtx, err := m.nativeInstanceNetworkContext(operationCtx, inst.Lease.Instance, generation)
	if err != nil {
		return err
	}
	return m.changePrivateNetworkService(networkCtx, inst, nc, identity.Service)
}

// Allocation precedes physical setup. Release it only after rollback has
// confirmed removal; failed cleanup keeps ownership fenced for detach/teardown.
func (m *Manager) rollbackPrivateNetworkAttachment(ctx context.Context, nc netns.Config, instance string) error {
	if err := m.removeNetworkLink(ctx, nc.Instance, nc.PrivateVethHost); err != nil {
		return fmt.Errorf("rollback private side-link: %w", err)
	}
	return m.releasePrivateNetworkMember(ctx, nc, instance, false)
}
