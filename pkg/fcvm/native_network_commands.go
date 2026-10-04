package fcvm

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/netns"
)

type nativeNetworkScopeKey struct{}
type nativeNetworkCommandScope struct {
	owner   nativeLaunchRecord
	purpose nativeHostHelperPurpose
}

func (m *Manager) nativeNetworkContext(ctx context.Context, instance, generation string, purpose nativeHostHelperPurpose) (context.Context, error) {
	v := m.nativeVMM()
	if v == nil {
		return ctx, nil
	}
	r := v.nativeRecoveryRuntime()
	owner, err := r.journal.read(instance)
	if err != nil {
		return nil, err
	}
	if generation != "" && generation != owner.Generation {
		return nil, errors.New("native network: original VM generation changed")
	}
	if purpose == nativeHostHelperNetworkCleanup {
		if !owner.Revoked || !owner.ExitConfirmed || owner.ResourcesRemoved {
			return nil, errors.New("native network: cleanup requires unfinished retired ownership")
		}
	} else if owner.Revoked || owner.ResourcesRemoved || r.generation(instance) != owner.Generation {
		return nil, errors.New("native network: instance has no current local producer")
	}
	return context.WithValue(ctx, nativeNetworkScopeKey{}, nativeNetworkCommandScope{owner: owner, purpose: purpose}), nil
}

func (m *Manager) nativeInstanceNetworkContext(ctx context.Context, instance, generation string) (context.Context, error) {
	if m.nativeVMM() != nil && generation == "" {
		return nil, errors.New("native network: live instance has no original generation")
	}
	return m.nativeNetworkContext(ctx, instance, generation, nativeHostHelperEffect)
}

func (m *Manager) stampNativeInstanceGeneration(inst *Instance) error {
	v := m.nativeVMM()
	if v == nil {
		return nil
	}
	r := v.nativeRecoveryRuntime()
	owner, err := r.journal.read(inst.Lease.Instance)
	if err != nil {
		return err
	}
	if !sameNativeJournalLease(owner.Lease, inst.Lease) || owner.Revoked || owner.ResourcesRemoved || r.generation(inst.Lease.Instance) != owner.Generation {
		return errors.New("native network: publication has no matching original VM owner")
	}
	inst.nativeGeneration = owner.Generation
	return nil
}

type nativeNetworkCommandRunner struct {
	vmm   nativeRecoveryVMM
	scope nativeNetworkCommandScope
}

func (r nativeNetworkCommandRunner) Run(ctx context.Context, argv []string) error {
	_, err := r.RunCapture(ctx, argv)
	return err
}
func (r nativeNetworkCommandRunner) RunInput(ctx context.Context, argv []string, input []byte) error {
	_, err := r.vmm.runNativeHostCommand(ctx, r.scope.owner, r.scope.purpose, argv, input)
	return err
}
func (r nativeNetworkCommandRunner) RunCapture(ctx context.Context, argv []string) ([]byte, error) {
	return r.vmm.runNativeHostCommand(ctx, r.scope.owner, r.scope.purpose, argv, nil)
}

func (m *Manager) networkCommandRunner(ctx context.Context) (Runner, error) {
	v := m.nativeVMM()
	if v == nil {
		return m.run, nil
	}
	scope, ok := ctx.Value(nativeNetworkScopeKey{}).(nativeNetworkCommandScope)
	if !ok {
		return nil, errors.New("native network: command lacks original lease scope")
	}
	return nativeNetworkCommandRunner{vmm: v, scope: scope}, nil
}

func (m *Manager) runNetworkCommand(ctx context.Context, argv []string) error {
	runner, err := m.networkCommandRunner(ctx)
	if err != nil {
		return err
	}
	return runner.Run(ctx, argv)
}

func (m *Manager) networkCaptureRunner(ctx context.Context) (CaptureRunner, error) {
	if m.nativeVMM() == nil {
		return m.captureRunner, nil
	}
	runner, err := m.networkCommandRunner(ctx)
	if err != nil {
		return nil, err
	}
	capture, ok := runner.(CaptureRunner)
	if !ok {
		return nil, fmt.Errorf("native network: capture capability unavailable")
	}
	return capture, nil
}

// Network deletion follows successful VM retirement. Recovered cleanup cannot
// borrow normal execution authority, and an already acknowledged lease does
// not create another helper even for an idempotent duplicate stop.
func (m *Manager) nativeCleanupNetworkContext(ctx context.Context, lease Lease) (context.Context, bool, error) {
	v := m.nativeVMM()
	if v == nil {
		return ctx, false, nil
	}
	owner, err := v.nativeRecoveryRuntime().journal.read(lease.Instance)
	if err != nil {
		return nil, false, err
	}
	if !sameNativeJournalLease(owner.Lease, lease) {
		return nil, false, errors.New("native network: cleanup lease changed")
	}
	if owner.ResourcesRemoved {
		return ctx, true, nil
	}
	ctx, err = m.nativeNetworkContext(ctx, lease.Instance, owner.Generation, nativeHostHelperNetworkCleanup)
	return ctx, false, err
}

// File fallback is an in-process producer, so hold the same launch lock across
// its effect instead of allowing retirement/replacement between check and remove.
func (m *Manager) removeScopedStaleNetnsMarker(ctx context.Context, name string) (err error) {
	v := m.nativeVMM()
	if v == nil {
		removeStaleNetnsMarker(name)
		return nil
	}
	scope, ok := ctx.Value(nativeNetworkScopeKey{}).(nativeNetworkCommandScope)
	if !ok || name != scope.owner.Lease.Netns || scope.purpose != nativeHostHelperEffect {
		return errors.New("native network: namespace marker lacks original ownership")
	}
	r := v.nativeRecoveryRuntime()
	lock, err := r.journal.lock(ctx, scope.owner.Lease.Instance)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, lock.Close()) }()
	owner, err := r.journal.read(scope.owner.Lease.Instance)
	if err != nil {
		return err
	}
	if owner.Generation != scope.owner.Generation || owner.KernelBootID != scope.owner.KernelBootID || !sameNativePhysicalLease(owner.Lease, scope.owner.Lease) || r.generation(owner.Lease.Instance) != owner.Generation {
		return errors.New("native network: namespace marker owner changed")
	}
	if err := validateNativeHostHelperAuthority(owner, scope.purpose, nil, false); err != nil {
		return err
	}
	helpers := nativeHostHelperJournal{owner: r.journal, groups: r.helperGroups}
	if err := helpers.requireRemoved(owner); err != nil {
		return err
	}
	removeStaleNetnsMarker(name)
	return nil
}

func (m *Manager) validateNativeNetworkConfig(ctx context.Context, nc netns.Config) error {
	if m.nativeVMM() == nil {
		return nil
	}
	scope, ok := ctx.Value(nativeNetworkScopeKey{}).(nativeNetworkCommandScope)
	if !ok {
		return errors.New("native network: config has no original scope")
	}
	owned := nativeLeaseNetwork(scope.owner.Lease)
	if scope.owner.Lease.Networkless || nc.Instance != owned.Instance || nc.Netns != owned.Netns || nc.VethHost != owned.VethHost || nc.VethPeer != owned.VethPeer || nc.HostIP != owned.HostIP || (nc.PrivateVethHost != "" && nc.PrivateVethHost != owned.PrivateVethHost) || (nc.PrivateVethPeer != "" && nc.PrivateVethPeer != owned.PrivateVethPeer) {
		return errors.New("native network: config differs from the original lease")
	}
	return nil
}
