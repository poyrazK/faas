package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) CreateDataRepairOperation(_ context.Context, in DataRepairAdmission) (Operation, bool, error) {
	if err := validateDataRepairAdmission(in); err != nil {
		return Operation{}, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.operationMemoryLocked()
	if prior, ok := d.operations[in.ID]; ok {
		if prior.AccountID != in.AccountID {
			return Operation{}, false, ErrNotFound
		}
		return cloneOperation(prior), false, replayDataRepair(prior, in)
	}
	ownerID := in.Plan.SourceOperationID
	if in.Plan.ReplacesPlanID != "" {
		ownerID = in.Plan.ReplacesPlanID
	}
	source, ok := d.operations[ownerID]
	if !ok {
		return Operation{}, false, ErrNotFound
	}
	app := m.apps[in.AppID]
	if app.AccountID != in.AccountID || app.Status == AppDeleted {
		return Operation{}, false, ErrNotFound
	}
	var parent Operation
	var parentEvent api.OperationEvent
	parentPending := false
	if in.Plan.ReplacesPlanID != "" {
		parent = cloneOperation(d.operations[in.Plan.ReplacesPlanID])
		parentPending = !parent.State.Terminal()
		var err error
		parentEvent, err = finishDataRepairReplacement(&parent, in, time.Now().UTC())
		if err != nil {
			return Operation{}, false, err
		}
	}
	limits := api.MustLimitsFor(m.accounts[in.AccountID].Plan).Operations
	if !limits.Allowed {
		return Operation{}, false, NewOperationLimitError("plan_admission", 0, 1)
	}
	pending := 0
	for _, existing := range d.operations {
		if existing.AccountID == in.AccountID && !existing.State.Terminal() {
			pending++
		}
	}
	if parentPending {
		pending-- // Child admission replaces its reserved nonterminal parent.
	}
	if pending >= limits.PendingPerAccount {
		return Operation{}, false, NewOperationLimitError("pending_per_account", int64(limits.PendingPerAccount), int64(pending)+1)
	}
	op, err := newDataRepairOperation(source, in, limits, time.Now().UTC())
	if err != nil {
		return Operation{}, false, err
	}
	d.operations[op.ID] = cloneOperation(op)
	event := initialOperationEvent(op)
	event.ExecutionID = op.ID
	d.events[op.ID] = []api.OperationEvent{event}
	if in.Plan.ReplacesPlanID != "" {
		m.saveDataRepairLocked(parent, parentEvent)
	}
	return cloneOperation(op), true, nil
}

func (m *MemStore) AdvanceDataRepairOperation(_ context.Context, account, id string, in DataRepairAdvance) (Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.operationMemoryLocked()
	op, ok := d.operations[id]
	if !ok || op.AccountID != account {
		return Operation{}, ErrNotFound
	}
	op = cloneOperation(op)
	event, err := advanceDataRepair(&op, in, time.Now().UTC())
	if err != nil {
		return Operation{}, err
	}
	m.saveDataRepairLocked(op, event)
	return cloneOperation(op), nil
}

func (m *MemStore) saveDataRepairLocked(op Operation, event api.OperationEvent) {
	d := m.operationMemoryLocked()
	d.operations[op.ID] = cloneOperation(op)
	d.events[op.ID] = append(d.events[op.ID], event)
}
