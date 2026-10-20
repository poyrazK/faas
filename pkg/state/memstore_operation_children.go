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
	parent, exists := data.operations[in.ParentID]
	if !exists || parent.AccountID != in.Authority.AccountID {
		return OperationChild{}, false, ErrNotFound
	}
	inv, exists := m.invocations[in.Authority.InvocationID]
	if !exists {
		return OperationChild{}, false, ErrNotFound
	}
	if err := ValidateOperationExecutionAuthority(parent, inv, in.Authority, time.Now().UTC()); err != nil {
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
