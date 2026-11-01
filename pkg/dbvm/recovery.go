package dbvm

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/fcvm"
)

// Database VMs outlive vmmd (ADR-1027). A vmmd restart, a release rollout
// or a crash, must not stop the databases on its node: every database
// would lose its sessions and replay its WAL, and a production database's
// standby node would see its primary go silent. So each VM's jail holds a
// record of what vmmd needs to take it over, and Recover adopts every
// VM whose record and Firecracker process it finds: its slot, its exit,
// its console and the relays its guest connects to. Anything else (a
// waiting spare, a jail without a record or without a live process) is
// cleaned up as before.

// vmRecordName is the record's file in the jail directory, outside the
// chroot, so neither Firecracker nor the guest can read or change it.
const vmRecordName = "gregale-vm.json"

// vmRecordVersion is the record's format.
const vmRecordVersion = 1

// launchVersion counts changes to how the runtime launches a VM that only
// a restart applies; a VM recorded with an older one is stale (Stale).
// 1: memory.high with the writeback burst, the console FIFO, the record.
const launchVersion = 1

// vmRecord is what a restarted vmmd needs to adopt a running VM.
type vmRecord struct {
	Version int `json:"version"`
	// DatabaseID is the database the VM serves, or a spare's own id.
	DatabaseID string `json:"database_id"`
	// Spare marks a spare no database has adopted; Recover stops it.
	Spare bool `json:"spare,omitempty"`
	Slot  int  `json:"slot"`
	Major int  `json:"major"`
	// Base is the base image the VM booted from.
	Base string `json:"base"`
	// Volume is the database's volume file on the host; SpareDir the
	// directory a spare-born VM reaches it through.
	Volume   string `json:"volume,omitempty"`
	SpareDir string `json:"spare_dir,omitempty"`
	// The archive relay's header fields.
	RestoreSource        string `json:"restore_source,omitempty"`
	RestoreWindowSeconds int64  `json:"restore_window_seconds,omitempty"`
	// A standby's primary, for its replication relay.
	PrimaryNode   string `json:"primary_node,omitempty"`
	PrimaryTarget string `json:"primary_target,omitempty"`
	// StandbyNode is, on a primary, the node whose standby may stream its
	// replication (SetStandbyNode).
	StandbyNode string `json:"standby_node,omitempty"`
	// LaunchVersion is the launchVersion the VM was started with.
	LaunchVersion int       `json:"launch_version"`
	BootedAt      time.Time `json:"booted_at"`
}

func (rec vmRecord) validate() error {
	switch {
	case rec.Version != vmRecordVersion:
		return fmt.Errorf("dbvm: record version %d", rec.Version)
	case validID(rec.DatabaseID) != nil:
		return errors.New("dbvm: record names no database")
	case rec.Slot < 0 || rec.Slot >= fcvm.MaxSlots:
		return fmt.Errorf("dbvm: record slot %d", rec.Slot)
	case rec.Major < dbguest.MinMajor || rec.Major > dbguest.MaxMajor:
		return fmt.Errorf("dbvm: record major %d", rec.Major)
	case rec.RestoreSource != "" && validID(rec.RestoreSource) != nil:
		return errors.New("dbvm: record restore source")
	case (rec.PrimaryNode == "") != (rec.PrimaryTarget == ""):
		return errors.New("dbvm: record primary")
	case !rec.Spare && rec.Volume == "":
		return errors.New("dbvm: record names no volume")
	case len(rec.StandbyNode) > 253:
		return errors.New("dbvm: record standby node")
	}
	return nil
}

// Stale reports whether a running VM runs an outdated base (a newer
// PostgreSQL minor or guest-init for its major) or launch, which only a
// restart replaces (ADR-1027). Unknown VMs and majors are not stale.
func (r *Runtime) Stale(id string) bool {
	vm, err := r.running(id)
	if err != nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if vm.waiting || vm.record.DatabaseID != id {
		return false
	}
	current, ok := r.cfg.Bases[vm.record.Major]
	return ok && (vm.base != current || vm.record.LaunchVersion < launchVersion)
}

// SetStandbyNode records which node's standby may stream this running
// primary's replication, so a restarted vmmd still admits it; "" forgets
// it.
func (r *Runtime) SetStandbyNode(id, node string) error {
	vm, err := r.running(id)
	if err != nil {
		return err
	}
	r.mu.Lock()
	rec := vm.record
	r.mu.Unlock()
	if rec.DatabaseID != id {
		return fmt.Errorf("dbvm: %s has no record", id)
	}
	rec.StandbyNode = node
	return r.recordVM(vm, rec)
}

// StandbyNodes maps each running primary with a standby to its standby's
// node, as SetStandbyNode recorded them.
func (r *Runtime) StandbyNodes() map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]string{}
	for id, vm := range r.vms {
		if vm.record.DatabaseID == id && vm.record.StandbyNode != "" {
			out[id] = vm.record.StandbyNode
		}
	}
	return out
}

// writeRecord replaces the jail's record atomically.
func writeRecord(dir string, rec vmRecord) error {
	rec.Version = vmRecordVersion
	if err := rec.validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, vmRecordName+".tmp")
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("dbvm: write record: %w", err)
	}
	if err := os.Rename(tmp, filepath.Join(dir, vmRecordName)); err != nil {
		return fmt.Errorf("dbvm: write record: %w", err)
	}
	return nil
}

// readRecord reads and validates the jail's record.
func readRecord(dir string) (vmRecord, error) {
	raw, err := os.ReadFile(filepath.Join(dir, vmRecordName))
	if err != nil {
		return vmRecord{}, err
	}
	var rec vmRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return vmRecord{}, fmt.Errorf("dbvm: read record: %w", err)
	}
	return rec, rec.validate()
}
