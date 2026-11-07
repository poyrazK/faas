// adr: 149 — amendment 2026-11-07: prepared networks under native recovery.
package fcvm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/netns"
)

// nativePreparedSpareVMM is the journal side of prepared network spares under
// native recovery. Every method is fenced by the spare's or target's launch
// record, as a VM launch is.
type nativePreparedSpareVMM interface {
	nativePreparedSparesEnabled() bool
	prepareNativeSpare(context.Context, Lease) (nativeLaunchRecord, error)
	claimNativeSpare(context.Context, nativeLaunchRecord, string) error
	moveNativeSpare(context.Context, string, func() error) error
	finishNativeClaim(context.Context, nativePreparedLink) error
	archiveNativeSpare(context.Context, Lease) error
}

// WithNativePreparedNetworks publishes prepared network spares and their
// claims as version-four records (ADR-149 amendment 2026-11-07). It needs
// native launch owner records; vmmd refuses the setting without them.
func (v *JailerVMM) WithNativePreparedNetworks() *JailerVMM {
	v.nativePreparedNetworks = true
	if v.nativeRecovery != nil {
		v.nativeRecovery.journal.preparedSpares = true
	}
	return v
}

func (v *JailerVMM) nativePreparedSparesEnabled() bool {
	r := v.nativeRecovery
	return r != nil && r.journal.preparedSpares && r.journal.owners
}

func (v *JailerVMM) prepareNativeSpare(ctx context.Context, lease Lease) (nativeLaunchRecord, error) {
	r := v.nativeRecovery
	if r == nil {
		return nativeLaunchRecord{}, errors.New("native prepared network: native recovery is off")
	}
	if err := r.acquireDaemonOwnership(ctx); err != nil {
		return nativeLaunchRecord{}, err
	}
	record, err := r.journal.prepareSpare(ctx, lease)
	if err != nil {
		return record, err
	}
	r.remember(record)
	return record, nil
}

func (v *JailerVMM) claimNativeSpare(ctx context.Context, spare nativeLaunchRecord, target string) error {
	r := v.nativeRecovery
	if r == nil || r.generation(spare.Lease.Instance) != spare.Generation {
		return errors.New("native prepared network: spare has no local producer")
	}
	_, err := r.journal.claimSpare(ctx, spare, target)
	return err
}

// moveNativeSpare runs move, the in-process namespace move, under the
// target's launch lock, for the target's current local generation only.
func (v *JailerVMM) moveNativeSpare(ctx context.Context, target string, move func() error) error {
	r := v.nativeRecovery
	if r == nil {
		return errors.New("native prepared network: native recovery is off")
	}
	generation := r.generation(target)
	if generation == "" {
		return errors.New("native prepared network: target has no local producer")
	}
	return r.journal.withPreparedTarget(ctx, target, generation, func(nativeLaunchRecord) error { return move() })
}

func (v *JailerVMM) finishNativeClaim(ctx context.Context, link nativePreparedLink) error {
	r := v.nativeRecovery
	if r == nil {
		return errors.New("native prepared network: native recovery is off")
	}
	if err := r.journal.finishClaim(ctx, link); err != nil {
		return err
	}
	r.mu.Lock()
	if r.owned[link.Source] == link.SourceGeneration {
		delete(r.owned, link.Source)
	}
	r.mu.Unlock()
	return nil
}

// archiveNativeSpare moves a cleaned-up spare's record out of the record
// root. Spares cycle through slots every half hour; a completed record left
// behind would name veths a later instance reuses.
func (v *JailerVMM) archiveNativeSpare(ctx context.Context, lease Lease) error {
	r := v.nativeRecovery
	if r == nil {
		return errors.New("native prepared network: native recovery is off")
	}
	return r.journal.archiveRetiredSpare(ctx, lease)
}

func (j *nativeLaunchJournal) archiveRetiredSpare(ctx context.Context, lease Lease) (err error) {
	lock, err := j.lock(ctx, lease.Instance)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, lock.Close()) }()
	record, err := j.read(lease.Instance)
	if errors.Is(err, os.ErrNotExist) {
		return nil // already archived
	}
	if err != nil {
		return err
	}
	if !record.spare() || !record.ResourcesRemoved || !sameNativeJournalLease(record.Lease, lease) {
		return errors.New("native prepared network: only a removed spare is archived")
	}
	archive := filepath.Join(j.root, "retired")
	if err := os.MkdirAll(archive, 0o700); err != nil {
		return err
	}
	if err := checkNativeJournalPath(archive, true); err != nil {
		return err
	}
	path := filepath.Join(archive, record.Generation+".json")
	if _, statErr := os.Lstat(path); errors.Is(statErr, os.ErrNotExist) {
		if err := writeNativeLaunchRecord(path, record); err != nil {
			return fmt.Errorf("native prepared network: archive removed spare: %w", err)
		}
	} else if statErr != nil {
		return statErr
	} else if archived, err := readNativeLaunchRecord(path, lease.Instance); err != nil || !sameArchivedSpare(archived, record) {
		return errors.Join(errors.New("native prepared network: spare archive differs from its record"), err)
	}
	if err := os.Remove(j.path(lease.Instance)); err != nil {
		return err
	}
	return syncNativeJournalDir(j.root)
}

// nativePreparedSourceNetwork is the spare-named network a claiming record
// also owns until its claim is finished.
func nativePreparedSourceNetwork(owner nativeLaunchRecord) (netns.Config, bool) {
	if owner.Prepared.Source == "" || owner.spare() {
		return netns.Config{}, false
	}
	source := owner.Lease
	source.Instance, source.Netns = owner.Prepared.Source, "fc-"+owner.Prepared.Source
	return nativeLeaseNetwork(source), true
}

// nativeSpares returns the native spare journal when the pool must use it.
func (m *Manager) nativeSpares() (nativePreparedSpareVMM, bool) {
	if m.nativeVMM() == nil {
		return nil, false
	}
	spares, ok := m.vmm.(nativePreparedSpareVMM)
	return spares, ok
}

// nativeTeardownCommands is lease's teardown plus, for a claim whose move
// never ran, the claimed spare's namespace.
func (m *Manager) nativeTeardownCommands(ctx context.Context, lease Lease) [][]string {
	cmds := nativeLeaseNetwork(lease).TeardownCommands()
	if scope, ok := ctx.Value(nativeNetworkScopeKey{}).(nativeNetworkCommandScope); ok {
		if source, linked := nativePreparedSourceNetwork(scope.owner); linked {
			cmds = append(cmds, source.TeardownCommands()...)
		}
	}
	return cmds
}

// retireNativeSpare retires an unclaimed or abandoned spare like a launch
// that never started, removes its objects under cleanup authority, and
// archives its record. A spare already archived is done.
func (m *Manager) retireNativeSpare(ctx context.Context, spares nativePreparedSpareVMM, lease Lease) error {
	v := m.nativeVMM()
	if v == nil {
		return errors.New("native prepared network: native recovery is off")
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), preparedNetworkTimeout)
	defer cancel()
	if _, err := v.nativeRecoveryRuntime().journal.read(lease.Instance); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if err := m.vmm.Kill(ctx, lease); err != nil {
		return fmt.Errorf("retire prepared spare %s: %w", lease.Instance, err)
	}
	networkCtx, skip, err := m.nativeCleanupNetworkContext(ctx, lease)
	if err != nil {
		return err
	}
	if !skip {
		for _, argv := range nativeLeaseNetwork(lease).TeardownCommands() {
			if err := m.runNetworkCommand(networkCtx, argv); err != nil {
				m.log.Debug("prepared spare teardown command", "cmd", argv, "err", err)
			}
		}
	}
	if err := v.confirmNativeCleanup(ctx, lease, nativeLeaseNetwork(lease)); err != nil {
		return err
	}
	return spares.archiveNativeSpare(ctx, lease)
}

// retireRecoveredNativeSpares runs once at startup, before the pool opens:
// recovery holds every unclaimed or abandoned spare's slot, and nothing else
// would ever stop them. A spare that cannot be retired keeps its slot
// quarantined and is retried at the next start.
func (m *Manager) retireRecoveredNativeSpares(ctx context.Context, spares nativePreparedSpareVMM) {
	m.mu.Lock()
	var leases []Lease
	for id, held := range m.nativeRecovered {
		if nativePreparedSpareName(id) {
			leases = append(leases, held...)
		}
	}
	m.mu.Unlock()
	for _, lease := range leases {
		if err := m.retireNativeSpare(ctx, spares, lease); err != nil {
			m.log.Error("prepared networks: recovered spare retained", "instance", lease.Instance, "err", err)
			continue
		}
		if _, err := m.releaseNativeRecovered(lease.Instance); err != nil {
			m.log.Error("prepared networks: recovered spare slot retained", "instance", lease.Instance, "err", err)
		}
	}
}

// completeNativePreparedClaim moves a claimed spare's namespace to its target
// once the target's linked record is durable, then finishes the claim. From
// the move on, the target owns every object; if finishing fails, the
// target's own cleanup finishes it.
func (m *Manager) completeNativePreparedClaim(ctx context.Context, lease Lease, entry *preparedNetworkEntry) error {
	spares, ok := m.nativeSpares()
	p := m.preparedNetworks
	if !ok || p == nil || entry.nativeClaim == nil {
		return errors.New("native prepared network: claim has no journal")
	}
	oldNS := entry.config.Netns
	if err := spares.moveNativeSpare(ctx, lease.Instance, func() error { return p.move(oldNS, lease.Netns) }); err != nil {
		return fmt.Errorf("prepared network: move claimed spare: %w", err)
	}
	entry.config.Instance, entry.config.Netns = lease.Instance, lease.Netns
	if err := spares.finishNativeClaim(ctx, *entry.nativeClaim); err != nil {
		m.log.Warn("prepared network claim left linked to its target", "instance", lease.Instance, "err", err)
	}
	entry.nativeClaim = nil
	return nil
}

// abandonNativePreparedClaim retires a claimed spare whose target never
// published its linked record: the spare still owns its objects.
func (m *Manager) abandonNativePreparedClaim(ctx context.Context, entry *preparedNetworkEntry) {
	spares, ok := m.nativeSpares()
	if !ok || entry.nativeClaim == nil {
		return
	}
	if err := m.retireNativeSpare(ctx, spares, entry.native.Lease); err != nil {
		m.log.Error("prepared network: abandoned spare retained", "instance", entry.native.Lease.Instance, "err", err)
	}
	entry.nativeClaim = nil
}
