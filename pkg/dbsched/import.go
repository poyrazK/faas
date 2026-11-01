package dbsched

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/dbguest"
)

// Importing another PostgreSQL database (ADR-1027 migration off Neon). The
// database's own VM copies the source: its node's vmmd relays the copy's
// connections to the source endpoint over verified TLS, and the guest
// restores it in one transaction. The import runs in the guest; schedd
// starts it, reports it, and keeps the database from parking meanwhile.

// ImportSpec is a source database and where it listens.
type ImportSpec struct {
	Host   string
	Port   int
	Source dbguest.ImportSource
}

// Validate rejects a spec vmmd or the guest would refuse.
func (s ImportSpec) Validate() error {
	if s.Host == "" || len(s.Host) > 253 || s.Port < 1 || s.Port > 65535 {
		return fmt.Errorf("%w: import source endpoint", ErrInvalid)
	}
	if err := s.Source.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return nil
}

// noImport is the status of a database that is not importing.
var noImport = dbguest.ImportStatus{State: dbguest.ImportNone}

// Import wakes the database and starts copying the source into it. A
// running or finished import is reported, not restarted.
func (s *Scheduler) Import(ctx context.Context, id string, spec ImportSpec) (dbguest.ImportStatus, error) {
	if err := spec.Validate(); err != nil {
		return noImport, err
	}
	vm, err := s.Wake(ctx, id)
	if err != nil {
		return noImport, err
	}
	node, err := s.node(ctx, vm.NodeID)
	if err != nil {
		return noImport, err
	}
	status, err := s.vmm.Import(ctx, node, id, spec)
	_ = s.Touch(ctx, id)
	return status, err
}

// ImportStatus reports a database's import without waking it, so a
// database that is not running reports none. An unfinished import did not
// survive its VM stopping (the restore is one transaction, so the target
// is empty again); a finished one is recorded in the target, and Import
// under the same operation reports it done.
func (s *Scheduler) ImportStatus(ctx context.Context, id string) (dbguest.ImportStatus, error) {
	vm, err := s.store.Get(ctx, id)
	if err != nil {
		return noImport, err
	}
	if vm.State != StateRunning {
		return noImport, nil
	}
	status, err := s.guestImport(ctx, vm)
	if errors.Is(err, ErrVMNotRunning) {
		return noImport, nil
	}
	if err == nil && status.State == dbguest.ImportRunning {
		_ = s.Touch(ctx, id)
	}
	return status, err
}

func (s *Scheduler) guestImport(ctx context.Context, vm VM) (dbguest.ImportStatus, error) {
	node, err := s.node(ctx, vm.NodeID)
	if err != nil {
		return noImport, err
	}
	resp, err := s.vmm.Control(ctx, node, vm.DatabaseID, dbguest.Request{Op: dbguest.OpImportStatus})
	if err != nil {
		return noImport, err
	}
	if resp.Import == nil {
		return noImport, nil
	}
	return *resp.Import, nil
}

// importing reports whether a running database is importing, so idle
// parking and eviction leave it alone: its import is its activity.
func (s *Scheduler) importing(ctx context.Context, vm VM) bool {
	status, err := s.guestImport(ctx, vm)
	return err == nil && status.State == dbguest.ImportRunning
}
