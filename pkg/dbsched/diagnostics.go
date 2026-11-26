package dbsched

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/dbguest"
)

func (s *Scheduler) Diagnose(ctx context.Context, id, database string) (dbguest.Diagnostics, error) {
	state, response, err := s.readDiagnostics(ctx, id, dbguest.Request{Op: dbguest.OpDiagnostics, DiagnosticDatabase: database})
	if state == dbguest.DiagnosticsAvailable && response.Activity == nil {
		state = dbguest.DiagnosticsUnavailable
	}
	return dbguest.Diagnostics{State: state, Sample: response.Activity}, err
}

func (s *Scheduler) TableHealth(ctx context.Context, id, database string, options dbguest.TableHealthOptions) (dbguest.TableHealth, error) {
	state, response, err := s.readDiagnostics(ctx, id, dbguest.Request{
		Op: dbguest.OpDiagnostics, DiagnosticDatabase: database, TableHealthOptions: &options,
	})
	if state == dbguest.DiagnosticsAvailable && response.TableHealth == nil {
		state = dbguest.DiagnosticsUnavailable
	}
	return dbguest.TableHealth{State: state, Sample: response.TableHealth}, err
}

// readDiagnostics uses only the current, running primary. It leaves the idle
// deadline untouched and discards samples that cross a move or state change.
func (s *Scheduler) readDiagnostics(ctx context.Context, id string, req dbguest.Request) (string, dbguest.Response, error) {
	vm, err := s.store.Get(ctx, id)
	if err != nil {
		return "", dbguest.Response{}, err
	}
	if vm.State == StateParked {
		return dbguest.DiagnosticsParked, dbguest.Response{}, nil
	}
	if vm.State != StateRunning || vm.Held || vm.MoveNode != "" {
		return dbguest.DiagnosticsUnavailable, dbguest.Response{}, nil
	}
	node, err := s.node(ctx, vm.NodeID)
	if err != nil {
		return dbguest.DiagnosticsUnavailable, dbguest.Response{}, nil
	}
	probe, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	response, err := s.vmm.Control(probe, node, id, req)
	current, readErr := s.store.Get(ctx, id)
	if readErr != nil {
		return "", dbguest.Response{}, readErr
	}
	if current.State == StateParked {
		return dbguest.DiagnosticsParked, dbguest.Response{}, nil
	}
	if err != nil || response.Error != "" || response.State != dbguest.StateReady ||
		current.State != StateRunning || current.Held || current.MoveNode != "" ||
		current.NodeID != vm.NodeID || !current.StateChangedAt.Equal(vm.StateChangedAt) {
		return dbguest.DiagnosticsUnavailable, dbguest.Response{}, nil
	}
	return dbguest.DiagnosticsAvailable, response, nil
}
