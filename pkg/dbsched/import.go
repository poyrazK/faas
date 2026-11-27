package dbsched

import (
	"context"
	"errors"
	"fmt"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/dbvm"
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
	if err := (dbvm.ImportSource{Host: s.Host, Port: s.Port}).Validate(); err != nil {
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

// CustomerImport inspects or starts a copy using the customer import op,
// which older vmmd or guest versions cannot accidentally start.
func (s *Scheduler) CustomerImport(ctx context.Context, id string, spec ImportSpec, checkOnly bool) (dbguest.ImportStatus, error) {
	client, ok := s.vmm.(interface {
		CustomerImport(context.Context, Node, string, ImportSpec, bool) (dbguest.ImportStatus, error)
	})
	if !ok {
		return noImport, ErrImportUnsupported
	}
	node, err := s.sourceInspectionNode(ctx, id, spec)
	if err != nil {
		return noImport, err
	}
	status, err := client.CustomerImport(ctx, node, id, spec, checkOnly)
	_ = s.Touch(ctx, id)
	return status, err
}

// CheckMigration uses the native relay without starting or reserving a copy.
func (s *Scheduler) CheckMigration(ctx context.Context, id string, spec ImportSpec) (*vmmdpb.DatabaseMigrationCheck, error) {
	client, ok := s.vmm.(interface {
		CheckMigration(context.Context, Node, string, ImportSpec) (*vmmdpb.DatabaseMigrationCheck, error)
	})
	if !ok {
		return nil, ErrImportUnsupported
	}
	node, err := s.sourceInspectionNode(ctx, id, spec)
	if err != nil {
		return nil, err
	}
	check, err := client.CheckMigration(ctx, node, id, spec)
	_ = s.Touch(ctx, id)
	return check, err
}

func (s *Scheduler) sourceInspectionNode(ctx context.Context, id string, spec ImportSpec) (Node, error) {
	if err := spec.Validate(); err != nil {
		return Node{}, err
	}
	vm, err := s.Wake(ctx, id)
	if err != nil {
		return Node{}, err
	}
	return s.node(ctx, vm.NodeID)
}

var ErrImportUnsupported = errors.New("dbsched: customer import unavailable")

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
	resp, err := s.guestImportStatus(ctx, vm)
	if err != nil || resp.Import == nil {
		return noImport, err
	}
	return *resp.Import, nil
}

func (s *Scheduler) guestImportStatus(ctx context.Context, vm VM) (dbguest.Response, error) {
	node, err := s.node(ctx, vm.NodeID)
	if err != nil {
		return dbguest.Response{}, err
	}
	return s.vmm.Control(ctx, node, vm.DatabaseID, dbguest.Request{Op: dbguest.OpImportStatus})
}

// importing protects database copies in progress from idle parking.
func (s *Scheduler) importing(ctx context.Context, vm VM) bool {
	resp, err := s.guestImportStatus(ctx, vm)
	return err == nil && (resp.MigrationRunning || resp.Import != nil && resp.Import.State == dbguest.ImportRunning)
}
