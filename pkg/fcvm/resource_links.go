// adr: 402
package fcvm

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"slices"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/netns"
)

// The address is committed before creation and supplied in the atomic link-add
// request. Only this running owner may use it to resolve an incomplete create.
type resourceLinkIdentity struct {
	Index   int    `json:"index"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Address string `json:"address"`
}

type ownedResourceLink struct {
	instance string
	asset    resourceAsset
}

func resourceLinkPath(name string) string { return filepath.Join("/sys/class/net", name) }

func validateResourceLink(r resourceJournalRecord, a resourceAsset) error {
	l := a.Link
	private, _ := privateVethNames(r.Lease.Slot)
	if r.Version < 4 || r.Lease.Networkless || l == nil || l.Index < 0 || l.Index > 1<<31-1 || l.Kind != "veth" ||
		(l.Name != r.Lease.VethHost && l.Name != private) || a.Path != resourceLinkPath(l.Name) ||
		!validResourceLinkAddress(l.Address) ||
		a.Namespace == nil || a.Namespace.MountID != 0 || a.File != nil || a.Target != nil || a.Source != "" ||
		a.SourceFile != nil || a.Mount != nil || a.ReadOnly || a.OriginalMode != 0 {
		return errors.New("invalid veth resource asset")
	}
	return nil
}

func newResourceLinkAddress() string {
	marker := uuid.New()
	marker[0] = (marker[0] | 2) &^ 1 // locally administered, unicast; 46 random bits
	return net.HardwareAddr(marker[:6]).String()
}

func validResourceLinkAddress(value string) bool {
	address, err := net.ParseMAC(value)
	return err == nil && len(address) == 6 && address[0]&3 == 2 && address.String() == value
}

func (j *ResourceJournal) checkpointLink(instance, path string, link resourceLinkIdentity) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return errResourceJournalClosed
	}
	r, ok := j.records[instance]
	if !ok {
		return errors.New("veth checkpoint requires lease intent")
	}
	r.Assets = cloneResourceAssets(r.Assets)
	for i := range r.Assets {
		a := &r.Assets[i]
		if a.Path != path {
			continue
		}
		if a.Kind != "veth" || a.Link == nil || a.Link.Index != 0 || link.Index <= 0 ||
			a.Link.Name != link.Name || a.Link.Kind != link.Kind || a.Link.Address != link.Address {
			return errors.New("veth checkpoint differs from intent")
		}
		a.Link = &link
		if err := r.validate(); err != nil {
			return err
		}
		return j.persist(r)
	}
	return errors.New("veth checkpoint requires asset intent")
}

func (m *Manager) rememberLink(instance string, a resourceAsset) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.resourceLinks == nil {
		m.resourceLinks = make(map[string]ownedResourceLink)
	}
	m.resourceLinks[a.Link.Name] = ownedResourceLink{instance: instance, asset: cloneResourceAssets([]resourceAsset{a})[0]}
}

func (m *Manager) linkOwner(name string) (ownedResourceLink, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.resourceLinks[name]
	o.asset = cloneResourceAssets([]resourceAsset{o.asset})[0]
	return o, ok
}

func (m *Manager) networkLinkNames(nc netns.Config) []string {
	names := []string{nc.VethHost, nc.PrivateVethHost}
	// A failed live attachment may not yet be in the cached Config. Its live
	// observation still belongs to this instance and must hold the slot.
	m.mu.Lock()
	for name, o := range m.resourceLinks {
		if o.instance == nc.Instance && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	m.mu.Unlock()
	return names
}

func (m *Manager) probeLink(name string, index int) (*resourceLinkIdentity, error) {
	if m.linkProbe != nil {
		return m.linkProbe(name, index)
	}
	return resourceNetworkLinkAt(name, index)
}

func (m *Manager) currentLinkContext() (*resourceMountIdentity, error) {
	if m.linkContext != nil {
		return m.linkContext()
	}
	return resourceNetworkContext()
}

func (m *Manager) checkOwnedLink(instance, name string) (*resourceLinkIdentity, error) {
	if name == "" {
		return nil, nil
	}
	o, owned := m.linkOwner(name)
	index := 0
	if owned {
		current, err := m.currentLinkContext()
		if err != nil || current == nil || o.asset.Namespace == nil || *current != *o.asset.Namespace || o.instance != instance {
			return nil, errors.Join(errors.New("veth creator context/owner changed or unknown"), err)
		}
		index = o.asset.Link.Index
	}
	actual, err := m.probeLink(name, index)
	if err != nil || actual == nil {
		return actual, err
	}
	if !owned || actual.Name != name || actual.Kind != "veth" || actual.Address != o.asset.Link.Address ||
		(index != 0 && actual.Index != index) {
		return nil, errors.New("veth identity changed or lacks a live owner")
	}
	return actual, nil
}

func (m *Manager) checkNetworkLinks(nc netns.Config) error {
	if m.resourceJournal == nil {
		return nil
	}
	for _, name := range m.networkLinkNames(nc) {
		if _, err := m.checkOwnedLink(nc.Instance, name); err != nil {
			return fmt.Errorf("check veth %s: %w", name, err)
		}
	}
	return nil
}

func (m *Manager) removeNetworkLink(ctx context.Context, instance, name string) error {
	if name == "" {
		return nil
	}
	if m.resourceJournal == nil {
		return m.run.Run(ctx, []string{"ip", "link", "del", name})
	}
	actual, err := m.checkOwnedLink(instance, name)
	if err != nil {
		return err
	}
	if actual != nil {
		remove := resourceNetworkLinkDelete
		if m.linkDelete != nil {
			remove = m.linkDelete
		}
		if err := remove(actual.Index); err != nil {
			return err
		}
	}
	if remaining, err := m.checkOwnedLink(instance, name); err != nil || remaining != nil {
		return errors.Join(errors.New("veth removal unconfirmed"), err)
	}
	journal, err := m.namespaceJournal(instance)
	if err != nil {
		return err
	}
	if journal != nil {
		if err := journal.retireAsset(instance, resourceLinkPath(name)); err != nil {
			return err
		}
	}
	m.mu.Lock()
	delete(m.resourceLinks, name)
	m.mu.Unlock()
	return nil
}

func (m *Manager) createNetworkLink(ctx context.Context, nc netns.Config, argv []string) error {
	name := argv[3]
	if actual, err := m.checkOwnedLink(nc.Instance, name); err != nil || actual != nil {
		return errors.Join(errors.New("veth creation requires an absent name"), err)
	}
	// Retire an absent partial intent before a new epoch uses the same name.
	if err := m.removeNetworkLink(ctx, nc.Instance, name); err != nil {
		return err
	}
	creator, err := m.currentLinkContext()
	if err != nil || creator == nil {
		return errors.Join(errors.New("capture veth creator context"), err)
	}
	a := resourceAsset{Kind: "veth", Path: resourceLinkPath(name), Namespace: creator,
		Link: &resourceLinkIdentity{Name: name, Kind: "veth", Address: newResourceLinkAddress()}}
	m.rememberLink(nc.Instance, a)
	journal, err := m.namespaceJournal(nc.Instance)
	if err != nil {
		return err
	}
	if journal != nil {
		if err := journal.addAsset(nc.Instance, a); err != nil {
			return err
		}
	}
	// Generic link attributes precede type-specific veth peer arguments.
	command := append(slices.Clone(argv[:4]), "address", a.Link.Address)
	command = append(command, argv[4:]...)
	if err := m.runCommands(ctx, [][]string{command}); err != nil {
		return err
	}
	actual, err := m.checkOwnedLink(nc.Instance, name)
	if err != nil || actual == nil {
		return errors.Join(errors.New("capture created veth"), err)
	}
	a.Link = actual
	m.rememberLink(nc.Instance, a)
	if journal != nil {
		return journal.checkpointLink(nc.Instance, a.Path, *actual)
	}
	return nil
}

// Flush batching at each create so its checkpoint precedes bridge attachment,
// peer movement, policy and guest start. Remaining setup keeps normal batching.
func (m *Manager) runJournalIPSetup(ctx context.Context, nc netns.Config, cmds [][]string) error {
	if m.resourceJournal == nil {
		return m.runIPSetupCommands(ctx, cmds)
	}
	start := 0
	for i, argv := range cmds {
		if len(argv) < 4 || !slices.Equal(argv[:3], []string{"ip", "link", "add"}) {
			continue
		}
		supported := len(argv) == 9 && slices.Equal(argv[4:8], []string{"type", "veth", "peer", "name"}) &&
			((argv[3] == nc.VethHost && argv[8] == nc.VethPeer) || (argv[3] == nc.PrivateVethHost && argv[8] == nc.PrivateVethPeer))
		if !supported {
			return errors.New("unsupported journaled link creation command")
		}
		if err := m.runIPSetupCommands(ctx, cmds[start:i]); err != nil {
			return err
		}
		if err := m.createNetworkLink(ctx, nc, argv); err != nil {
			return err
		}
		start = i + 1
	}
	return m.runIPSetupCommands(ctx, cmds[start:])
}

func (m *Manager) transferNetworkLinks(oldInstance string, nc netns.Config) error {
	if m.resourceJournal == nil {
		return nil
	}
	old := nc
	old.Instance = oldInstance
	if err := m.checkNetworkLinks(old); err != nil {
		return err
	}
	m.mu.Lock()
	for name, o := range m.resourceLinks {
		if o.instance == oldInstance {
			o.instance = nc.Instance
			m.resourceLinks[name] = o
		}
	}
	m.mu.Unlock()
	return nil
}

func (m *Manager) checkpointPreparedLinks(nc netns.Config) error {
	if m.resourceJournal == nil {
		return nil
	}
	if err := m.checkNetworkLinks(nc); err != nil {
		return err
	}
	for _, name := range m.networkLinkNames(nc) {
		if name == "" {
			continue
		}
		if actual, err := m.checkOwnedLink(nc.Instance, name); err != nil || actual == nil {
			return errors.Join(errors.New("prepared veth disappeared before checkpoint"), err)
		}
		o, ok := m.linkOwner(name)
		if !ok {
			if name == nc.VethHost && name != "" {
				return errors.New("prepared veth lacks a live owner")
			}
			continue
		}
		if o.asset.Link.Index == 0 {
			return errors.New("prepared veth lacks a creation checkpoint")
		}
		intent := cloneResourceAssets([]resourceAsset{o.asset})[0]
		intent.Link.Index = 0
		if err := m.resourceJournal.addAsset(nc.Instance, intent); err != nil {
			return err
		}
		if err := m.resourceJournal.checkpointLink(nc.Instance, intent.Path, *o.asset.Link); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) checkPreparedNetwork(nc netns.Config) error {
	if m.resourceJournal == nil {
		return nil
	}
	if err := errors.Join(m.checkOwnedNamespace(nc.Netns), m.checkNetworkLinks(nc)); err != nil {
		return err
	}
	if ns, err := m.probeNamespace(nc.Netns); err != nil || ns == nil {
		return errors.Join(errors.New("prepared namespace disappeared"), err)
	}
	link, err := m.checkOwnedLink(nc.Instance, nc.VethHost)
	o, owned := m.linkOwner(nc.VethHost)
	if err != nil || link == nil || !owned || o.asset.Link.Index == 0 {
		return errors.Join(errors.New("prepared veth lost its checkpoint"), err)
	}
	return nil
}

func (m *Manager) teardownJournalNetwork(ctx context.Context, nc netns.Config) error {
	if err := m.checkOwnedNamespace(nc.Netns); err != nil {
		return err
	}
	if err := m.checkNetworkLinks(nc); err != nil {
		return err
	}
	// Delete host ends first: namespace deletion can implicitly destroy peers.
	for _, name := range m.networkLinkNames(nc) {
		if err := m.removeNetworkLink(ctx, nc.Instance, name); err != nil {
			return err
		}
	}
	if err := m.checkOwnedNamespace(nc.Netns); err != nil {
		return err
	}
	if err := m.run.Run(ctx, []string{"ip", "netns", "del", nc.Netns}); err != nil {
		m.log.Debug("owned namespace deletion", "netns", nc.Netns, "err", err)
	}
	return m.retireOwnedNamespace(nc)
}
