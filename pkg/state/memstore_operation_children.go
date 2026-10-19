// adr: 20261018183000
package state

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type memOperationChild struct {
	parent, key, child string
	createdAt          time.Time
}

func (m *MemStore) StartChildOperation(_ context.Context, in OperationChildStart) (OperationChild, bool, error) {
	if err := validateOperationChildRequest(in.Request); err != nil {
		return OperationChild{}, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	data := m.operationMemoryLocked()
	parent, err := m.lockedChildParent(in)
	if err != nil {
		return OperationChild{}, false, err
	}
	_, isChild := data.childParents[parent.ID]
	if err := checkOperationChildParent(parent, isChild); err != nil {
		return OperationChild{}, false, err
	}
	var childDef OperationDefinition
	for _, def := range data.definitions {
		if def.AccountID == parent.AccountID && def.AppID == parent.AppID && def.DeploymentID == parent.DeploymentID && def.Spec.Name == in.Request.Operation {
			childDef = def
		}
	}
	if childDef.ID == "" {
		return OperationChild{}, false, ErrNotFound
	}
	known, replay := data.children[parent.ID][in.Request.Key]
	if !replay {
		limit := api.MustLimitsFor(m.accounts[parent.AccountID].Plan).Operations.ChildrenPerOperation
		if started := len(data.children[parent.ID]); started >= limit {
			return OperationChild{}, false, NewOperationLimitError("children_per_operation", int64(limit), int64(started)+1)
		}
	}
	child, _, err := m.admitOperationLocked(operationChildAdmission(parent, childDef.ID, in.Request), nil)
	if err != nil {
		return OperationChild{}, false, err
	}
	if replay {
		if known.child != child.ID {
			return OperationChild{}, false, fmt.Errorf("%w: key names an expired child", ErrConflict)
		}
		return OperationChild{Key: in.Request.Key, Operation: child}, false, nil
	}
	if data.children == nil {
		data.children, data.childParents = map[string]map[string]memOperationChild{}, map[string]string{}
	}
	if data.children[parent.ID] == nil {
		data.children[parent.ID] = map[string]memOperationChild{}
	}
	data.children[parent.ID][in.Request.Key] = memOperationChild{parent: parent.ID, key: in.Request.Key, child: child.ID, createdAt: child.CreatedAt}
	data.childParents[child.ID] = parent.ID
	return OperationChild{Key: in.Request.Key, Operation: child}, true, nil
}

// lockedChildParent checks the presented proof against the parent's current
// execution.
func (m *MemStore) lockedChildParent(in OperationChildStart) (Operation, error) {
	if in.WorkflowAuthority != nil {
		parent, _, _, err := m.workflowOperationAuthorityLocked(in.ParentID, *in.WorkflowAuthority, false)
		if err == nil && parent.AccountID != in.accountID() {
			err = ErrNotFound
		}
		return parent, err
	}
	if in.JobAuthority != nil {
		parent, _, err := m.jobOperationAuthorityLocked(in.ParentID, *in.JobAuthority)
		if err == nil && parent.AccountID != in.accountID() {
			err = ErrNotFound
		}
		return parent, err
	}
	parent, exists := m.operationMemoryLocked().operations[in.ParentID]
	if !exists || parent.AccountID != in.accountID() {
		return Operation{}, ErrNotFound
	}
	inv, exists := m.invocations[in.Authority.InvocationID]
	if !exists {
		return Operation{}, ErrNotFound
	}
	return parent, ValidateOperationExecutionAuthority(parent, inv, in.Authority, time.Now().UTC())
}

func (m *MemStore) OperationChildren(ctx context.Context, accountID, tenantID, parentID string) ([]OperationChild, error) {
	if _, err := m.OperationByID(ctx, accountID, tenantID, parentID); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	data := m.operationMemoryLocked()
	rows := make([]memOperationChild, 0, len(data.children[parentID]))
	for _, row := range data.children[parentID] {
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].createdAt.Equal(rows[j].createdAt) {
			return rows[i].createdAt.Before(rows[j].createdAt)
		}
		return rows[i].key < rows[j].key
	})
	out := make([]OperationChild, 0, len(rows))
	for _, row := range rows {
		if op, ok := data.operations[row.child]; ok {
			out = append(out, OperationChild{Key: row.key, Operation: cloneOperation(m.operationDeliveryLocked(op))})
		}
	}
	return out, nil
}

func (m *MemStore) OperationChildSettlements(_ context.Context, limit int) ([]OperationChildSettlement, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data := m.operationMemoryLocked()
	out := []OperationChildSettlement{}
	for _, op := range data.operations {
		if op.State != api.OperationWaiting || op.Waiting == nil || op.Waiting.Kind != api.OperationWaitEvent || op.Waiting.Event != api.OperationChildrenSettledEvent {
			continue
		}
		if m.workflowEventNamedLocked(op.WorkflowRunID, api.OperationChildrenSettledEvent) {
			continue // signalled; the scheduler has yet to resume it
		}
		settled, counts := true, api.OperationChildrenSettled{}
		for _, row := range data.children[op.ID] {
			child := data.operations[row.child]
			counts.Total++
			switch child.State {
			case api.OperationSucceeded:
				counts.Succeeded++
			case api.OperationFailed:
				counts.Failed++
			case api.OperationCancelled:
				counts.Cancelled++
			default:
				settled = false
			}
		}
		if settled {
			out = append(out, OperationChildSettlement{AccountID: op.AccountID, ParentID: op.ID, Children: counts})
		}
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (m *MemStore) workflowEventNamedLocked(runID, name string) bool {
	for _, event := range m.workflowEvents[runID] {
		if event.EventName == name {
			return true
		}
	}
	return false
}
