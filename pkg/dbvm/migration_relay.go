package dbvm

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/dbmigration"
)

type migrationBinding struct {
	Operation string
	Endpoint  ImportSource
}

func (r *Runtime) migrationBindingPath(id string) string {
	return r.VolumePath(id) + ".migration.json"
}

func (r *Runtime) readMigrationBinding(id string) (migrationBinding, error) {
	raw, err := os.ReadFile(r.migrationBindingPath(id))
	if err != nil {
		return migrationBinding{}, err
	}
	var binding migrationBinding
	if err := json.Unmarshal(raw, &binding); err != nil || !dbguest.ValidOperationID(binding.Operation) || binding.Endpoint.Validate() != nil {
		return migrationBinding{}, errors.New("dbvm: invalid migration binding")
	}
	return binding, nil
}

func (r *Runtime) saveMigrationBinding(id string, binding migrationBinding) error {
	dir := filepath.Dir(r.migrationBindingPath(id))
	f, err := os.CreateTemp(dir, ".migration-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := json.NewEncoder(f).Encode(binding); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), r.migrationBindingPath(id)); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func (r *Runtime) deleteMigrationBinding(id string) error {
	err := os.Remove(r.migrationBindingPath(id))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (r *Runtime) serveMigrationRelay(vm *databaseVM, endpoint ImportSource) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if vm.migrationLn == nil {
		path := r.udsPath(vm) + "_" + strconv.FormatUint(uint64(dbguest.MigrationPort), 10)
		ln, err := r.listenSourceRelay(vm, path, vm.migrationSrc.Load)
		if err != nil {
			return err
		}
		vm.migrationLn = ln
	}
	vm.migrationSrc.Store(&endpoint)
	return nil
}

func (r *Runtime) restoreMigrationRelay(id string, vm *databaseVM) {
	binding, err := r.readMigrationBinding(id)
	if err == nil {
		err = r.serveMigrationRelay(vm, binding.Endpoint)
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		r.logWarn("dbvm: restore migration relay", "database", id, "err", err)
	}
}

// ControlMigration reserves a dedicated relay for the target and operation.
// The host persists the endpoint only. The guest verifies replication
// identity and owns the receipt, password and target fence.
func (r *Runtime) ControlMigration(ctx context.Context, id string, src ImportSource, req dbguest.Request) (dbguest.Response, error) {
	if src.Validate() != nil || req.Validate() != nil || req.MigrationPin != "" ||
		!req.Op.IsMigrationMutation() {
		return dbguest.Response{}, ErrInvalidImportSource
	}
	if addr, err := netip.ParseAddr(src.Host); err == nil {
		src.Host = addr.String()
	} else {
		src.Host = strings.ToLower(src.Host)
	}
	vm, err := r.running(id)
	if err != nil {
		return dbguest.Response{}, err
	}
	if !vm.importMu.TryLock() {
		return dbguest.Response{}, ErrImportBusy
	}
	defer vm.importMu.Unlock()
	if time.Now().Before(vm.importUncertainUntil) {
		return dbguest.Response{}, ErrImportBusy
	}
	if err := ctx.Err(); err != nil {
		return dbguest.Response{}, err
	}
	local, cancel := context.WithTimeout(context.WithoutCancel(ctx), 35*time.Second)
	defer cancel()
	socket := r.udsPath(vm)
	binding := migrationBinding{Operation: req.Import.OperationID, Endpoint: src}
	saved, err := r.readMigrationBinding(id)
	fresh := errors.Is(err, os.ErrNotExist)
	if !fresh && err != nil {
		return dbguest.Response{}, err
	}
	if fresh || saved != binding {
		st, err := Control(local, socket, dbguest.Request{Op: dbguest.OpMigrationStatus})
		if err != nil {
			return st, err
		}
		if st.Error != "" || st.MigrationProgress == nil {
			return st, nil
		}
		if op := st.MigrationProgress.OperationID; op != "" && (!fresh || op != binding.Operation) {
			return dbguest.Response{Error: dbguest.ErrCodeImportOperationMismatch}, nil
		}
		fresh = true // Uncommitted reservations can be replaced after a guest probe.
		if err := r.saveMigrationBinding(id, binding); err != nil {
			return dbguest.Response{}, err
		}
	}
	if err := r.serveMigrationRelay(vm, src); err != nil {
		return dbguest.Response{}, err
	}
	req.MigrationPin = dbmigration.EndpointPin(id, src.Host, src.Port)
	st, err := Control(local, socket, req)
	if err == nil && fresh && (!st.MigrationFenced || st.Error != "") {
		vm.migrationSrc.Store(nil)
		if err := r.deleteMigrationBinding(id); err != nil {
			return st, err
		}
	}
	if st.MigrationProgress != nil && (st.MigrationProgress.State == dbmigration.Stopped ||
		req.Op == dbguest.OpMigrationStop && st.MigrationProgress.State == dbmigration.Activated) {
		vm.migrationSrc.Store(nil)
	}
	return st, err
}
