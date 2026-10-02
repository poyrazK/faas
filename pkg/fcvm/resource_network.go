// adr: 401
package fcvm

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/netns"
)

func (m *Manager) probeNamespace(name string) (*resourceAsset, error) {
	if m.namespaceProbe != nil {
		return m.namespaceProbe(name)
	}
	return resourceNetworkNamespaceAt(name)
}

func (m *Manager) rememberNamespace(name string, a resourceAsset) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.resourceNetworks == nil {
		m.resourceNetworks = make(map[string]resourceAsset)
	}
	m.resourceNetworks[name] = a
}

func (m *Manager) namespaceOwner(name string) (resourceAsset, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.resourceNetworks[name]
	return cloneResourceAssets([]resourceAsset{a})[0], ok
}

func namespaceCheckpointMatches(a, b resourceAsset) bool {
	return a.File != nil && b.File != nil && *a.File == *b.File &&
		a.Mount != nil && b.Mount != nil && *a.Mount == *b.Mount
}

func (m *Manager) moveNamespaceObservation(oldName, newName string) error {
	if m.resourceJournal == nil {
		return nil
	}
	old, ok := m.namespaceOwner(oldName)
	// The alias mount ID changes; the nsfs inode and creator context must not.
	uncertain := old
	uncertain.Path, uncertain.Mount = filepath.Join("/run/netns", newName), nil
	m.rememberNamespace(newName, uncertain)
	actual, err := m.probeNamespace(newName)
	if err != nil || !ok || actual == nil || old.File == nil || actual.File == nil || *old.File != *actual.File || old.Namespace == nil || actual.Namespace == nil || *old.Namespace != *actual.Namespace {
		return errors.Join(errors.New("prepared namespace changed during move"), err)
	}
	m.rememberNamespace(newName, *actual)
	m.mu.Lock()
	delete(m.resourceNetworks, oldName)
	m.mu.Unlock()
	return nil
}

func (m *Manager) checkOwnedNamespace(name string) error {
	if m.resourceJournal == nil {
		return nil
	}
	owned, ok := m.namespaceOwner(name)
	if ok {
		if err := m.checkNamespaceContext(owned); err != nil {
			return err
		}
	}
	actual, err := m.probeNamespace(name)
	if err != nil || actual == nil {
		return err
	}
	if !ok || !namespaceCheckpointMatches(owned, *actual) {
		return errors.New("network namespace binding changed or lacks a live owner")
	}
	return nil
}

// Absence is meaningful only in the same creator context, even on retry.
func (m *Manager) checkNamespaceContext(owned resourceAsset) error {
	probe := resourcePlacementContext
	if m.namespaceContext != nil {
		probe = m.namespaceContext
	}
	current, err := probe()
	if err != nil {
		return err
	}
	if current == nil || owned.Namespace == nil || *current != *owned.Namespace {
		return errors.New("network namespace creator context changed or unknown")
	}
	return nil
}

// Prepared spares have no guest lease record. Their live observations stay in
// memory until claim; startup inventory still quarantines any renamed survivor.
func (m *Manager) namespaceJournal(instance string) (*ResourceJournal, error) {
	if m.resourceJournal == nil {
		return nil, nil
	}
	_, ok, err := m.resourceJournal.lookup(instance)
	if err != nil || !ok {
		return nil, err
	}
	return m.resourceJournal, nil
}

func (m *Manager) retireOwnedNamespace(nc netns.Config) error {
	if m.resourceJournal == nil {
		return nil
	}
	if err := m.checkOwnedNamespace(nc.Netns); err != nil {
		return err
	}
	if actual, err := m.probeNamespace(nc.Netns); err != nil || actual != nil {
		return errors.Join(errors.New("namespace removal unconfirmed"), err)
	}
	if _, owned := m.namespaceOwner(nc.Netns); owned && m.namespaceProbe == nil {
		if err := syncResourceParent(filepath.Join("/run/netns", nc.Netns)); err != nil {
			return err
		}
	}
	journal, err := m.namespaceJournal(nc.Instance)
	if err != nil {
		return err
	}
	if journal != nil {
		if err := journal.retireAsset(nc.Instance, filepath.Join("/run/netns", nc.Netns)); err != nil {
			return err
		}
	}
	m.mu.Lock()
	delete(m.resourceNetworks, nc.Netns)
	m.mu.Unlock()
	return nil
}

func (m *Manager) setupJournalNetwork(ctx context.Context, nc netns.Config) error {
	if err := m.checkOwnedNamespace(nc.Netns); err != nil {
		return err
	}
	if err := m.removeNamespaceForRebuild(ctx, nc); err != nil {
		return err
	}
	contextProbe := resourcePlacementContext
	if m.namespaceContext != nil {
		contextProbe = m.namespaceContext
	}
	context, err := contextProbe()
	if err != nil {
		return err
	}
	a := resourceAsset{Kind: "netns", Path: filepath.Join("/run/netns", nc.Netns), Namespace: context}
	m.rememberNamespace(nc.Netns, a)
	journal, err := m.namespaceJournal(nc.Instance)
	if err != nil {
		return err
	}
	if journal != nil {
		if err := journal.addAsset(nc.Instance, a); err != nil {
			return err
		}
	}
	cmds := nc.SetupCommands()
	if err := m.runCommands(ctx, cmds[:1]); err != nil {
		return err
	}
	if err := m.checkpointCreatedNamespace(nc, journal); err != nil {
		return err
	}
	if err := m.runIPSetupCommands(ctx, cmds[1:]); err != nil {
		return err
	}
	if nc.EgressMbit > 0 {
		if err := m.runCommands(ctx, nc.TcCommands()); err != nil {
			return fmt.Errorf("tc egress cap: %w", err)
		}
	}
	return m.runNftCommands(ctx, nc.Netns, nc.NftCommands())
}

func (m *Manager) checkpointCreatedNamespace(nc netns.Config, journal *ResourceJournal) error {
	a, err := m.probeNamespace(nc.Netns)
	if err != nil || a == nil {
		return errors.Join(errors.New("capture created namespace"), err)
	}
	m.rememberNamespace(nc.Netns, *a)
	if journal == nil {
		return nil
	}
	return journal.checkpointAsset(nc.Instance, a.Path, *a.File, a.Mount)
}

func (m *Manager) removeNamespaceForRebuild(ctx context.Context, nc netns.Config) error {
	// Never destroy an unobserved stale binding. Namespace creation is strict.
	if _, ok := m.namespaceOwner(nc.Netns); !ok {
		return nil
	}
	for _, argv := range nc.TeardownCommands() {
		if err := m.run.Run(ctx, argv); err != nil {
			m.log.Debug("owned network rebuild teardown", "cmd", argv, "err", err)
		}
	}
	if err := networkRemoved(nc); err != nil {
		return err
	}
	return m.retireOwnedNamespace(nc)
}

func (m *Manager) checkpointPreparedNamespace(nc netns.Config) error {
	if m.resourceJournal == nil {
		return nil
	}
	if err := m.checkOwnedNamespace(nc.Netns); err != nil {
		return err
	}
	a, ok := m.namespaceOwner(nc.Netns)
	if !ok || a.File == nil || a.Mount == nil {
		return errors.New("prepared namespace lost its live checkpoint")
	}
	intent := a
	intent.File, intent.Mount = nil, nil
	if err := m.resourceJournal.addAsset(nc.Instance, intent); err != nil {
		return err
	}
	return m.resourceJournal.checkpointAsset(nc.Instance, a.Path, *a.File, a.Mount)
}
