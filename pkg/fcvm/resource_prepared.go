// adr: 403
// adr: 405
package fcvm

import (
	"errors"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/netns"
)

// Source is the permanent disk-record key. Target is committed before alias
// movement; Lease stays the spare until Wake commits its complete guest intent.
type resourcePreparedNetwork struct {
	Source string `json:"source"`
	Target string `json:"target,omitempty"`
	// Version 6 commits the creator boot before any physical setup starts.
	BootID string `json:"boot_id,omitempty"`
}

func (r resourceJournalRecord) storageIdentity() string {
	if r.Prepared != nil {
		return r.Prepared.Source
	}
	return r.Lease.Instance
}

func (r resourceJournalRecord) preparedSpare() bool {
	return r.Prepared != nil && r.Lease.Instance == r.Prepared.Source
}

func (r resourceJournalRecord) validatePrepared() error {
	if r.Prepared == nil {
		if r.Version == 5 || r.Version == 6 {
			return errors.New("prepared journal version requires prepared identity")
		}
		return nil
	}
	p := r.Prepared
	id, err := uuid.Parse(strings.TrimPrefix(p.Source, "prepared-"))
	if err != nil || p.Source != "prepared-"+id.String() || (r.Version != 5 && r.Version != 6) || r.Lease.Networkless ||
		(p.Target != "" && (!restartResourceID(p.Target) || len(p.Target) > 64 || strings.HasPrefix(p.Target, "prepared-"))) ||
		(r.Lease.Instance != p.Source && r.Lease.Instance != p.Target) {
		return errors.New("invalid prepared network record identity")
	}
	if r.Version == 5 && p.BootID != "" {
		return errors.New("legacy prepared record cannot carry creator boot")
	}
	if r.Version == 6 {
		boot, err := uuid.Parse(p.BootID)
		if err != nil || p.BootID != boot.String() {
			return errors.New("prepared record requires canonical creator boot")
		}
		if r.Process != nil && r.Process.BootID != p.BootID {
			return errors.New("prepared process differs from creator boot")
		}
		for _, a := range r.Assets {
			for _, context := range []*resourceMountIdentity{a.Namespace, a.Mount} {
				if context != nil && context.BootID != p.BootID {
					return errors.New("prepared asset differs from creator boot")
				}
			}
		}
	}
	if r.preparedSpare() {
		if r.Lease != leaseForSlot(p.Source, r.Lease.Slot) || r.Process != nil || len(r.Assets) > 2 {
			return errors.New("prepared spare cannot carry guest state")
		}
		for _, a := range r.Assets {
			if a.Kind != "netns" && (a.Kind != "veth" || a.Link == nil || a.Link.Name != r.Lease.VethHost) {
				return errors.New("invalid prepared spare asset")
			}
		}
	}
	return nil
}

func (j *ResourceJournal) recordForInstance(instance string) (resourceJournalRecord, bool) {
	if r, ok := j.records[instance]; ok {
		return r, true
	}
	for _, r := range j.records {
		if r.Prepared != nil && (r.Prepared.Source == instance || r.Prepared.Target == instance) {
			return r, true
		}
	}
	return resourceJournalRecord{}, false
}

func (j *ResourceJournal) beginPrepared(l Lease, bootID string) error {
	return j.beginRecord(resourceJournalRecord{Version: 6, Lease: l, Prepared: &resourcePreparedNetwork{Source: l.Instance, BootID: bootID}})
}

func (j *ResourceJournal) transferPrepared(source, target string) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return errResourceJournalClosed
	}
	r, ok := j.records[source]
	if !ok || !r.preparedSpare() || r.Prepared.Target != "" {
		return errors.New("prepared transfer requires an unclaimed spare")
	}
	if _, exists := j.recordForInstance(target); exists {
		return errors.New("prepared target already recorded")
	}
	if _, _, err := preparedRecordAssets(r); err != nil {
		return err
	}
	r.Prepared = &resourcePreparedNetwork{Source: source, Target: target, BootID: r.Prepared.BootID}
	if err := r.validate(); err != nil {
		return err
	}
	return j.persist(r)
}

func preparedRecordAssets(r resourceJournalRecord) (resourceAsset, resourceAsset, error) {
	var ns, link resourceAsset
	for _, a := range r.Assets {
		if a.Kind == "netns" {
			ns = a
		}
		if a.Kind == "veth" && a.Link != nil && a.Link.Name == r.Lease.VethHost {
			link = a
		}
	}
	if len(r.Assets) != 2 || ns.File == nil || ns.Mount == nil || ns.Namespace == nil || link.Link == nil || link.Link.Index == 0 || link.Namespace == nil {
		return ns, link, errors.New("prepared network lacks complete creation checkpoints")
	}
	return ns, link, nil
}

func (j *ResourceJournal) adoptPrepared(l Lease, ns, link resourceAsset) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return errResourceJournalClosed
	}
	r, ok := j.recordForInstance(l.Instance)
	if !ok || !r.preparedSpare() || r.Prepared.Target != l.Instance || r.Lease.Slot != l.Slot {
		return errors.New("prepared adoption requires matching transfer intent")
	}
	oldNS, oldLink, err := preparedRecordAssets(r)
	if err != nil {
		return err
	}
	if ns.File == nil || ns.Mount == nil || ns.Namespace == nil || link.Link == nil || link.Namespace == nil ||
		*ns.File != *oldNS.File || *ns.Namespace != *oldNS.Namespace || ns.Mount.BootID != oldNS.Mount.BootID || ns.Mount.Namespace != oldNS.Mount.Namespace ||
		*link.Link != *oldLink.Link || *link.Namespace != *oldLink.Namespace {
		return errors.New("prepared adoption observations changed")
	}
	r.Lease = l
	r.Assets = cloneResourceAssets([]resourceAsset{ns, link})
	if err := r.validate(); err != nil {
		return err
	}
	return j.persist(r)
}

func pendingPreparedLeaseMatches(r resourceJournalRecord, l Lease) bool {
	if !r.preparedSpare() || r.Prepared.Target != l.Instance || r.Lease.Slot != l.Slot {
		return false
	}
	want := leaseForSlot(l.Instance, l.Slot)
	return l.UID == want.UID && l.GID == want.GID && l.HostIP == r.Lease.HostIP && l.Netns == want.Netns && l.VethHost == want.VethHost && l.VethPeer == want.VethPeer
}

// Only the live pool calls this after guarded removal, before returning a slot.
func (j *ResourceJournal) forgetPrepared(l Lease) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return errResourceJournalClosed
	}
	r, ok := j.recordForInstance(l.Instance)
	if !ok {
		return nil
	}
	want := leaseForSlot(l.Instance, l.Slot)
	if r.Prepared == nil || r.Process != nil || r.Lease.Slot != l.Slot || l.UID != want.UID || l.GID != want.GID || l.HostIP != r.Lease.HostIP || l.Netns != want.Netns || l.VethHost != want.VethHost || l.VethPeer != want.VethPeer {
		return errors.New("prepared retirement identity differs")
	}
	return j.forgetRecord(r)
}

func (m *Manager) adoptPreparedJournal(l Lease) (bool, error) {
	r, ok, err := m.resourceJournal.lookup(l.Instance)
	if err != nil || !ok || !r.preparedSpare() {
		return false, err
	}
	nc := netns.NewConfig(l.Instance, l.Netns, l.VethHost, l.VethPeer, l.HostIP)
	if err := m.checkPreparedNetwork(nc); err != nil {
		return true, err
	}
	ns, _ := m.namespaceOwner(l.Netns)
	link, _ := m.linkOwner(l.VethHost)
	return true, m.resourceJournal.adoptPrepared(l, ns, link.asset)
}

func (m *Manager) preparedCheckpointCommitted(nc netns.Config) (bool, error) {
	r, ok, err := m.resourceJournal.lookup(nc.Instance)
	if err != nil || !ok || r.Prepared == nil {
		return false, err
	}
	if r.preparedSpare() {
		return true, errors.New("prepared guest intent not committed")
	}
	if err := m.checkPreparedNetwork(nc); err != nil {
		return true, err
	}
	ns, _ := m.namespaceOwner(nc.Netns)
	link, _ := m.linkOwner(nc.VethHost)
	for _, a := range r.Assets {
		if a.Path == filepath.Join("/run/netns", nc.Netns) && namespaceCheckpointMatches(a, ns) && a.Namespace != nil && *a.Namespace == *ns.Namespace {
			for _, b := range r.Assets {
				if b.Kind == "veth" && b.Link != nil && b.Namespace != nil && *b.Link == *link.asset.Link && *b.Namespace == *link.asset.Namespace {
					return true, nil
				}
			}
		}
	}
	return true, errors.New("prepared guest checkpoint changed or missing")
}

func preparedTarget(r resourceJournalRecord) string {
	if r.Prepared != nil {
		return r.Prepared.Target
	}
	return ""
}
